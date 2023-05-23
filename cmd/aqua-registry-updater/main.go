package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/aqua-registry-updater/pkg/controller"
	"github.com/suzuki-shunsuke/aqua-registry-updater/pkg/log"
	"github.com/suzuki-shunsuke/logrus-error/logerr"
)

var version = ""

func main() {
	logE := log.New(version)
	if err := core(context.Background(), logE); err != nil {
		logerr.WithError(logE, err).Fatal("aqua-registry-updater failed")
	}
}

func core(ctx context.Context, logE *logrus.Entry) error {
	token := os.Getenv("GITHUB_TOKEN")
	ctrl := controller.New(afero.NewOsFs(), controller.NewGitHub(ctx, token).Repositories)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ctrl.Update(ctx, logE, &controller.Param{ //nolint:wrapcheck
		GitHubToken: token,
	})
}
