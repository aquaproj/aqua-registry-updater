package controller

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

func (ctrl *Controller) Init(ctx context.Context, logE *logrus.Entry, param *Param) error {
	cfg := &Config{}
	if err := ctrl.readConfig("aqua-registry-updater.yaml", cfg); err != nil {
		return err
	}
	if err := cfg.SetDefault(ctrl.param.RepoOwner + "/" + ctrl.param.RepoName); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	// Get data from GHCR
	repo, err := ctrl.newRepo(cfg.ContainerRegistry, param.GitHubToken)
	if err != nil {
		return fmt.Errorf("create a client for a remote repository: %w", err)
	}

	const tag = "latest"

	data := &Data{}

	if err := ctrl.writeData("data.json", data); err != nil {
		return fmt.Errorf("update data.json: %w", err)
	}

	if err := pushFiles(ctx, repo, tag); err != nil {
		return err
	}

	return nil
}
