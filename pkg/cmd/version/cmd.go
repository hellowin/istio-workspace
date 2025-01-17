package version

import (
	"fmt"
	"runtime"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/maistra/istio-workspace/pkg/log"
	"github.com/maistra/istio-workspace/version"
)

var logger = func() logr.Logger {
	return log.Log.WithValues("type", "version")
}

// NewCmd creates version cmd which prints version and Build details of the executed binary.
func NewCmd() *cobra.Command {
	var short bool
	versionCmd := &cobra.Command{
		Use:          "version",
		Short:        "Prints the version number of ike cli",
		Long:         "All software has versions. This is Ike's",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if short {
				logShortVersion()
			} else {
				LogVersion()
			}

			return nil
		},
	}
	versionCmd.Flags().BoolVarP(&short, "short", "s", false, "prints only version without build details")

	return versionCmd
}

func logShortVersion() {
	logger().Info(version.Version)
}

func LogVersion() {
	logger().Info(fmt.Sprintf("Ike Version: %s", version.Version))
	logger().Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	logger().Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	logger().Info(fmt.Sprintf("Build Commit: %v", version.Commit))
	logger().Info(fmt.Sprintf("Build Time: %v", version.BuildTime))
}
