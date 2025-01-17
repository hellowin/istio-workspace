package e2e_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/maistra/istio-workspace/e2e/infra"
	"github.com/maistra/istio-workspace/test"
	testshell "github.com/maistra/istio-workspace/test/shell"
)

var _ = Describe("Smoke End To End Tests - Faulty scenarios", func() {

	Context("exit codes", func() {

		It("should return non 0 on failed command", func() {
			completion := testshell.ExecuteInDir(".", "bash", "-c", "ike missing-command")
			<-completion.Done()
			Expect(completion.Status().Exit).Should(Equal(23))
		})

	})

	Context("using ike cli", func() {

		var (
			namespace,
			tmpDir string
		)

		JustBeforeEach(func() {
			namespace = generateNamespaceName()
			tmpDir = test.TmpDir(GinkgoT(), "namespace-"+namespace)

			<-testshell.Execute(NewProjectCmd(namespace)).Done()

			PrepareEnv(namespace)
			InstallLocalOperator(namespace)
			Eventually(AllDeploymentsAndPodsReady(namespace), 10*time.Minute, 5*time.Second).Should(BeTrue())
		})

		AfterEach(func() {
			if CurrentGinkgoTestDescription().Failed {
				DumpEnvironmentDebugInfo(namespace, tmpDir)
			}
			cleanupNamespace(namespace)
		})

		Describe("session cleanup", func() {

			It("should remove session if non-existing deployment is specified", func() {

				// when
				ikeWithWatch := testshell.ExecuteInDir(tmpDir, "ike", "develop",
					"--deployment", "non-existing-deployment",
					"-n", namespace,
					"--port", "9080",
					"--method", "inject-tcp",
					"--watch",
					"--run", "python ratings.py 9080",
					"--route", "header:x-test-suite=smoke",
				)
				Eventually(ikeWithWatch.Done(), 10*time.Minute).Should(BeClosed())
				Expect(ikeWithWatch.Status().Exit).ToNot(Equal(0))

				// then
				Eventually(func() string {
					session := testshell.ExecuteInDir(tmpDir, "kubectl", "get", "sessions", "-n", namespace)
					<-session.Done()

					return strings.Join(session.Status().Stderr, " ")
				}, 10*time.Minute, 5*time.Second).Should(ContainSubstring("No resources found"))

			})

		})
	})
})
