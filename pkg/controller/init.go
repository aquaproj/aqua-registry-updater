package controller

import (
	"context"
	"fmt"
	"log/slog"
)

func (c *Controller) Init(ctx context.Context, _ *slog.Logger, param *Param) error {
	cfg := &Config{}
	if err := c.readConfig("aqua-registry-updater.yaml", cfg); err != nil {
		return err
	}
	if err := cfg.SetDefault(c.param.RepoOwner + "/" + c.param.RepoName); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	// Get data from GHCR
	repo, err := c.newRepo(cfg.ContainerRegistry, param.GitHubToken)
	if err != nil {
		return fmt.Errorf("create a client for a remote repository: %w", err)
	}

	const tag = "latest"

	data := &Data{}

	if err := c.writeData("data.json", data); err != nil {
		return fmt.Errorf("update data.json: %w", err)
	}

	if err := pushFiles(ctx, repo, tag); err != nil {
		return err
	}

	return nil
}
