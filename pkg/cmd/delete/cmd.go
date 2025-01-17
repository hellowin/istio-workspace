package delete //nolint:predeclared //reason to be discussed if we should replace it with remove instead

import (
	"emperror.dev/errors"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/maistra/istio-workspace/pkg/cmd/config"
	internal "github.com/maistra/istio-workspace/pkg/cmd/internal/session"
	"github.com/maistra/istio-workspace/pkg/log"
)

var logger = func() logr.Logger {
	return log.Log.WithValues("type", "delete")
}

// NewCmd creates instance of "create" Cobra Command with flags and execution logic defined.
func NewCmd() *cobra.Command {
	deleteCmd := &cobra.Command{
		Use:          "delete",
		Short:        "Deletes an existing Session",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return errors.Wrap(config.SyncFullyQualifiedFlags(cmd), "failed syncing flags")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, remove, err := internal.RemoveSessions(cmd)
			if err == nil {
				remove()
			}

			return errors.WrapIf(err, "failed to remove session")
		},
	}

	deleteCmd.Flags().StringP("deployment", "d", "", "name of the deployment or deployment config")
	deleteCmd.Flags().StringP("session", "s", "", "create or join an existing session")
	deleteCmd.Flags().StringP("namespace", "n", "", "target namespace to develop against "+
		"(defaults to default for the current context)")
	deleteCmd.Flags().Bool("offline", false, "avoid calling external sources")
	if err := deleteCmd.Flags().MarkHidden("offline"); err != nil {
		logger().Error(err, "failed while trying to hide a flag")
	}

	deleteCmd.Flags().VisitAll(config.BindFullyQualifiedFlag(deleteCmd))

	_ = deleteCmd.MarkFlagRequired("deployment")
	_ = deleteCmd.MarkFlagRequired("session")

	return deleteCmd
}
