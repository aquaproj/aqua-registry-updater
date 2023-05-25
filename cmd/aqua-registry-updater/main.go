package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
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
	crToken := os.Getenv("AQUA_REGISTRY_UPDATER_CONTAINER_REGISTRY_TOKEN")
	token := os.Getenv("GITHUB_TOKEN")
	repoOwner, repoName, found := strings.Cut(os.Getenv("GITHUB_REPOSITORY"), "/")
	if !found {
		return errors.New("GITHUB_REPOSITORY should include /")
	}
	ctrl := controller.New(afero.NewOsFs(), &controller.ParamNew{
		RepoOwner: repoOwner,
		RepoName:  repoName,
	}, controller.NewGitHub(ctx, token).PullRequests)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ctrl.Update(ctx, logE, &controller.Param{ //nolint:wrapcheck
		GitHubToken: crToken,
	})
}
