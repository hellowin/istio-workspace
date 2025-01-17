package istio

import (
	"fmt"
	"strings"

	"emperror.dev/errors"
	"istio.io/api/networking/v1alpha3"
	istionetwork "istio.io/client-go/pkg/apis/networking/v1alpha3"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maistra/istio-workspace/pkg/model"
	"github.com/maistra/istio-workspace/pkg/reference"
)

const (
	// VirtualServiceKind is the k8s Kind for a istio VirtualService.
	VirtualServiceKind = "VirtualService"

	// LabelIkeMutated is a bool label to indicated we own the resource.
	LabelIkeMutated = "ike.mutated"

	// LabelIkeMutatedValue is the bool value of the LabelIkeMutated label.
	LabelIkeMutatedValue = "true"
)

var (
	_                  model.Mutator     = VirtualServiceMutator
	_                  model.Revertor    = VirtualServiceRevertor
	_                  model.Manipulator = virtualServiceManipulator{}
	errorRouteNotFound                   = fmt.Errorf("route not found")
)

// VirtualServiceManipulator represents a model.Manipulator implementation for handling VirtualService objects.
func VirtualServiceManipulator() model.Manipulator {
	return virtualServiceManipulator{}
}

type virtualServiceManipulator struct {
}

func (d virtualServiceManipulator) TargetResourceType() client.Object {
	return &istionetwork.VirtualService{}
}
func (d virtualServiceManipulator) Mutate() model.Mutator {
	return VirtualServiceMutator
}
func (d virtualServiceManipulator) Revert() model.Revertor {
	return VirtualServiceRevertor
}

// VirtualServiceMutator attempts to create a virtual service for forked service.
func VirtualServiceMutator(ctx model.SessionContext, ref *model.Ref) error { //nolint:gocyclo,cyclop //reason it is what it is :D
	targetVersion := ref.GetVersion()

	vss, err := getVirtualServices(ctx, ctx.Namespace)
	if err != nil {
		ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, ctx.Namespace, model.ActionLocated, err.Error()))

		return err
	}

	var errs []error
	for _, hostName := range ref.GetTargetHostNames() {
		for _, vs := range vss.Items { //nolint:gocritic //reason for readability
			_, connected := connectedToGateway(vs)

			if !connected || vs.Labels[LabelIkeMutated] == LabelIkeMutatedValue {
				continue
			}
			ctx.Log.Info("Found VirtualService", "name", vs.Name)
			mutatedVs := mutateConnectedVirtualService(ctx, ref, hostName, vs)
			err = ctx.Client.Create(ctx, &mutatedVs)
			if err != nil && !k8sErrors.IsAlreadyExists(err) {
				ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, mutatedVs.Name, model.ActionCreated, err.Error()))
				errs = append(errs, errors.WrapIfWithDetails(err, "failed creating virtual service", "kind", VirtualServiceKind, "name", mutatedVs.Name, "host", hostName.String()))

				continue
			}
			ref.AddResourceStatus(model.NewSuccessResource(VirtualServiceKind, mutatedVs.Name, model.ActionCreated))
		}
		for _, vs := range vss.Items { //nolint:gocritic //reason for readability
			if !mutationRequired(vs, hostName, targetVersion) || vsAlreadyMutated(vs, hostName, ref.GetNewVersion(ctx.Name)) {
				continue
			}
			ctx.Log.Info("Found VirtualService", "name", vs.Name)
			mutatedVs, err := mutateVirtualService(ctx, ref, hostName, vs)
			if err != nil {
				ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, vs.Name, model.ActionModified, err.Error()))
				errs = append(errs, errors.WrapIfWithDetails(err, "failed mutating virtual service", "kind", VirtualServiceKind, "name", mutatedVs.Name, "host", hostName.String()))

				continue
			}

			if err = reference.Add(ctx.ToNamespacedName(), &mutatedVs); err != nil {
				ctx.Log.Error(err, "failed to add relation reference", "kind", mutatedVs.Kind, "name", mutatedVs.Name)
			}
			err = ctx.Client.Update(ctx, &mutatedVs)
			if err != nil {
				ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, mutatedVs.Name, model.ActionModified, err.Error()))
				errs = append(errs, errors.WrapIfWithDetails(err, "failed updating virtual service", "kind", VirtualServiceKind, "name", mutatedVs.Name, "host", hostName.String()))

				continue
			}
			ref.AddResourceStatus(model.NewSuccessResource(VirtualServiceKind, mutatedVs.Name, model.ActionModified))
		}
	}

	return errors.WrapIfWithDetails(
		errors.Combine(errs...),
		"failed to manipulate virtual service for session", "session", ctx.Name, "namespace", ctx.Namespace, "ref", ref.KindName.Name)
}

// VirtualServiceRevertor looks at the Ref.ResourceStatus and attempts to revert the state of the mutated objects.
func VirtualServiceRevertor(ctx model.SessionContext, ref *model.Ref) error {
	var errs []error
	resources := ref.GetResources(model.Kind(VirtualServiceKind))
	for _, resource := range resources {
		vs, err := getVirtualService(ctx, ctx.Namespace, resource.Name)
		if err != nil {
			if k8sErrors.IsNotFound(err) { // Not found, nothing to clean
				continue
			}
			ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, resource.Name, resource.Action, err.Error()))

			continue
		}
		ctx.Log.Info("Found VirtualService", "name", resource.Name)

		switch resource.Action { //nolint:exhaustive //reason only these cases are relevant
		case model.ActionModified:
			mutatedVs := revertVirtualService(ref.GetNewVersion(ctx.Name), *vs)
			if err = reference.Remove(ctx.ToNamespacedName(), &mutatedVs); err != nil {
				ctx.Log.Error(err, "failed to add relation reference", "kind", mutatedVs.Kind, "name", mutatedVs.Name)
			}
			err = ctx.Client.Update(ctx, &mutatedVs)
			if err != nil {
				ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, resource.Name, resource.Action, err.Error()))
				errs = append(errs, errors.WrapWithDetails(err, "failed updating VirtualService", "kind", VirtualServiceKind, "name", vs.Name))

				continue
			}
		case model.ActionCreated:
			err = ctx.Client.Delete(ctx, vs)
			if err != nil {
				ref.AddResourceStatus(model.NewFailedResource(VirtualServiceKind, resource.Name, resource.Action, err.Error()))
				errs = append(errs, errors.WrapWithDetails(err, "failed deleting VirtualService", "kind", VirtualServiceKind, "name", vs.Name))

				continue
			}
		}

		// ok, removed
		ref.RemoveResourceStatus(model.ResourceStatus{Kind: VirtualServiceKind, Name: resource.Name})
	}

	return errors.WrapIfWithDetails(
		errors.Combine(errs...),
		"failed to revert virtual service for session", "session", ctx.Name, "namespace", ctx.Namespace, "ref", ref.KindName.Name)
}

func mutateVirtualService(ctx model.SessionContext, ref *model.Ref, hostName model.HostName, source istionetwork.VirtualService) (istionetwork.VirtualService, error) {
	version := ref.GetVersion()
	newVersion := ref.GetNewVersion(ctx.Name)
	target := source.DeepCopy()
	clonedSource := source.DeepCopy()

	targetsHTTP := findRoutes(clonedSource, hostName, version)
	if len(targetsHTTP) == 0 {
		return istionetwork.VirtualService{}, errorRouteNotFound
	}
	for _, tHTTP := range targetsHTTP {
		simplifyTargetRoute(ctx, *tHTTP, hostName, version, newVersion, target)
	}

	return *target, nil
}

func mutateConnectedVirtualService(ctx model.SessionContext, ref *model.Ref, hostName model.HostName, source istionetwork.VirtualService) istionetwork.VirtualService {
	version := ref.GetVersion()
	newVersion := ref.GetNewVersion(ctx.Name)
	target := source.DeepCopy()
	clonedSource := source.DeepCopy()
	gateways, _ := connectedToGateway(*target)
	hosts := getHostsFromRef(ctx, gateways, ref)

	target.SetName(target.Name + "-" + ctx.Name)
	target.Spec.Hosts = hosts
	target.ResourceVersion = ""
	if target.Labels == nil {
		target.Labels = map[string]string{}
	}
	target.Labels[LabelIkeMutated] = LabelIkeMutatedValue

	targetsHTTP := findRoutes(clonedSource, hostName, version)
	for _, tHTTP := range targetsHTTP {
		simplifyTargetRouteWithoutMatch(*tHTTP, hostName, version, newVersion, target)
	}
	for i := 0; i < len(target.Spec.Http); i++ {
		targetHTTP := addHeaderRequest(*target.Spec.Http[i], ctx.Route)
		target.Spec.Http[i] = &targetHTTP
	}

	return *target
}

func simplifyTargetRouteWithoutMatch(targetHTTP v1alpha3.HTTPRoute, hostName model.HostName, version, newVersion string, target *istionetwork.VirtualService) {
	targetHTTP = removeOtherRoutes(targetHTTP, hostName, version)
	targetHTTP = updateSubset(targetHTTP, newVersion)
	targetHTTP = removeWeight(targetHTTP)
	targetHTTP.Mirror = nil
	targetHTTP.Redirect = nil

	target.Spec.Http = append([]*v1alpha3.HTTPRoute{&targetHTTP}, target.Spec.Http...)
}

func simplifyTargetRoute(ctx model.SessionContext, targetHTTP v1alpha3.HTTPRoute, hostName model.HostName, version, newVersion string, target *istionetwork.VirtualService) {
	targetHTTP = removeOtherRoutes(targetHTTP, hostName, version)
	targetHTTP = updateSubset(targetHTTP, newVersion)
	targetHTTP = addHeaderMatch(targetHTTP, ctx.Route)
	targetHTTP = removeWeight(targetHTTP)
	targetHTTP.Mirror = nil
	targetHTTP.Redirect = nil

	target.Spec.Http = append([]*v1alpha3.HTTPRoute{&targetHTTP}, target.Spec.Http...)
}

func revertVirtualService(subsetName string, vs istionetwork.VirtualService) istionetwork.VirtualService {
	for i := 0; i < len(vs.Spec.Http); i++ {
		http := vs.Spec.Http[i]
		for n := 0; n < len(http.Route); n++ {
			if strings.Contains(http.Route[n].Destination.Subset, subsetName) {
				vs.Spec.Http = append(vs.Spec.Http[:i], vs.Spec.Http[i+1:]...)
				i--

				break
			}
		}
	}

	return vs
}

func getVirtualService(ctx model.SessionContext, namespace, name string) (*istionetwork.VirtualService, error) {
	virtualService := istionetwork.VirtualService{}
	err := ctx.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &virtualService)

	return &virtualService, errors.WrapWithDetails(err, "failed finding virtual service in namespace", "name", name, "namespace", namespace)
}

func getVirtualServices(ctx model.SessionContext, namespace string) (*istionetwork.VirtualServiceList, error) {
	virtualServices := istionetwork.VirtualServiceList{}
	err := ctx.Client.List(ctx, &virtualServices, client.InNamespace(namespace))

	return &virtualServices, errors.WrapWithDetails(err, "failed finding virtual services in namespace", "namespace", namespace)
}

func mutationRequired(vs istionetwork.VirtualService, targetHost model.HostName, targetVersion string) bool {
	for _, http := range vs.Spec.Http {
		for _, route := range http.Route {
			if route.Destination != nil && targetHost.Match(route.Destination.Host) {
				if route.Destination.Subset == "" || route.Destination.Subset == targetVersion {
					return true
				}
			}
		}
	}

	return false
}

func vsAlreadyMutated(vs istionetwork.VirtualService, targetHost model.HostName, targetVersion string) bool {
	for _, http := range vs.Spec.Http {
		for _, route := range http.Route {
			if route.Destination != nil && targetHost.Match(route.Destination.Host) && route.Destination.Subset == targetVersion {
				return true
			}
		}
	}

	return false
}

func connectedToGateway(vs istionetwork.VirtualService) ([]string, bool) {
	return vs.Spec.Gateways, len(vs.Spec.Gateways) > 0
}

func findRoutes(vs *istionetwork.VirtualService, host model.HostName, subset string) []*v1alpha3.HTTPRoute {
	var routes []*v1alpha3.HTTPRoute
	for _, h := range vs.Spec.Http {
		for _, r := range h.Route {
			if r.Destination != nil && host.Match(r.Destination.Host) && (r.Destination.Subset == "" || r.Destination.Subset == subset) {
				routes = append(routes, h)
			}
		}
	}

	return routes
}

func removeOtherRoutes(http v1alpha3.HTTPRoute, host model.HostName, subset string) v1alpha3.HTTPRoute {
	for i, r := range http.Route {
		if !((r.Destination != nil && host.Match(r.Destination.Host) && r.Destination.Subset == subset) ||
			(r.Destination != nil && host.Match(r.Destination.Host) && r.Destination.Subset == "")) {
			http.Route = append(http.Route[:i], http.Route[i+1:]...)
		}
	}

	return http
}

func updateSubset(http v1alpha3.HTTPRoute, subset string) v1alpha3.HTTPRoute {
	for _, r := range http.Route {
		r.Destination.Subset = subset
	}

	return http
}

func addHeaderMatch(http v1alpha3.HTTPRoute, route model.Route) v1alpha3.HTTPRoute {
	addHeader := func(m *v1alpha3.HTTPMatchRequest, route model.Route) {
		if route.Type == "header" {
			if m.Headers == nil {
				m.Headers = map[string]*v1alpha3.StringMatch{}
			}
			m.Headers[route.Name] = &v1alpha3.StringMatch{MatchType: &v1alpha3.StringMatch_Exact{Exact: route.Value}}
		}
	}
	if len(http.Match) > 0 {
		for _, m := range http.Match {
			addHeader(m, route)
		}
	} else {
		m := &v1alpha3.HTTPMatchRequest{}
		addHeader(m, route)
		http.Match = append(http.Match, m)
	}

	return http
}

func addHeaderRequest(http v1alpha3.HTTPRoute, route model.Route) v1alpha3.HTTPRoute {
	if http.Headers == nil {
		http.Headers = &v1alpha3.Headers{
			Request: &v1alpha3.Headers_HeaderOperations{
				Add: map[string]string{},
			},
		}
	}
	if http.Headers.Request == nil {
		http.Headers.Request = &v1alpha3.Headers_HeaderOperations{
			Add: map[string]string{},
		}
	}
	http.Headers.Request.Add[route.Name] = route.Value

	return http
}

func removeWeight(http v1alpha3.HTTPRoute) v1alpha3.HTTPRoute {
	for _, r := range http.Route {
		r.Weight = 0
	}

	return http
}

func getHostsFromRef(ctx model.SessionContext, gateways []string, ref *model.Ref) []string {
	var hosts []string
	for _, gateway := range gateways {
		for _, gwTarget := range ref.GetTargets(model.All(model.Kind(GatewayKind), model.Name(gateway))) {
			for _, host := range strings.Split(gwTarget.Labels[LabelIkeHosts], ",") {
				hosts = append(hosts, ctx.Name+"."+host)
			}
		}
	}

	return hosts
}
