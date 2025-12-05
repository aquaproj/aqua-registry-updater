package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/aquaproj/aqua-registry-updater/pkg/controller"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
	"github.com/suzuki-shunsuke/slog-util/slogutil"
)

var version = ""

func main() {
	if code := core(); code != 0 {
		os.Exit(code)
	}
}

func core() int {
	logger := slogutil.New(&slogutil.InputNew{
		Name:    "aqua-registry-updater",
		Version: version,
		Out:     os.Stderr,
	})
	ctx := context.Background()
	crToken := os.Getenv("AQUA_REGISTRY_UPDATER_CONTAINER_REGISTRY_TOKEN")
	token := os.Getenv("GITHUB_TOKEN")
	repoOwner, repoName, found := strings.Cut(os.Getenv("GITHUB_REPOSITORY"), "/")
	if !found {
		logger.Error("GITHUB_REPOSITORY should include /")
		return 1
	}
	ctrl := controller.New(afero.NewOsFs(), &controller.ParamNew{
		RepoOwner: repoOwner,
		RepoName:  repoName,
	}, controller.NewGitHub(ctx, token).PullRequests)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ctrl.Update(ctx, logger, &controller.Param{
		GitHubToken: crToken,
		Args:        os.Args[1:],
	}); err != nil {
		slogerr.WithError(logger, err).Error("aqua-registry-updater failed")
		return 1
	}
	return 0
}
