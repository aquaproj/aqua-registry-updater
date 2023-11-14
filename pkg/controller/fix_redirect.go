package controller

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/aquaproj/registry-tool/pkg/checkrepo"
	genrg "github.com/aquaproj/registry-tool/pkg/generate-registry"
	"github.com/aquaproj/registry-tool/pkg/mv"
	"github.com/sirupsen/logrus"
)

func (c *Controller) fixRedirect(ctx context.Context, logE *logrus.Entry, pkg *Package, cfg *Config) (bool, error) {
	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	redirect, err := checkrepo.CheckRedirect(ctx, c.fs, httpClient, pkg.Name)
	if err != nil {
		return false, fmt.Errorf("check if the repository was transferred: %w", err)
	}
	if redirect == nil {
		return false, nil
	}
	logE.WithFields(logrus.Fields{
		"repo_owner": redirect.NewRepoOwner,
		"repo_name":  redirect.NewRepoName,
	}).Info("the package's repository was transferred")
	if err := mv.Move(ctx, c.fs, pkg.Name, redirect.NewPackageName); err != nil {
		return false, fmt.Errorf("rename a package: %w", err)
	}
	if err := genrg.GenerateRegistry(); err != nil {
		return false, fmt.Errorf("update registry.yaml: %w", err)
	}
	if err := c.createFixRedirectPR(ctx, pkg.Name, cfg, redirect); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Controller) createFixRedirectPR(ctx context.Context, pkgName string, cfg *Config, redirect *checkrepo.Redirect) error {
	paramTemplates := &ParamTemplates{
		PackageName:    pkgName,
		RepoOwner:      redirect.RepoOwner,
		RepoName:       redirect.RepoName,
		NewRepoOwner:   redirect.NewRepoOwner,
		NewRepoName:    redirect.NewRepoName,
		NewPackageName: redirect.NewPackageName,
	}

	prTitle, err := renderTemplate(cfg.compiledTemplates.TransferPRTitle, paramTemplates)
	if err != nil {
		return fmt.Errorf("render a template pr_title: %w", err)
	}

	prBody, err := renderTemplate(cfg.compiledTemplates.TransferPRBody, paramTemplates)
	if err != nil {
		return fmt.Errorf("render a template pr_body: %w", err)
	}

	pkgDir := filepath.Join("pkgs", filepath.FromSlash(redirect.NewPackageName))
	oldPkgDir := filepath.Join("pkgs", filepath.FromSlash(pkgName))
	branch := fmt.Sprintf("aqua-registry-updater-transfer-%s-", pkgName)
	if err := c.exec(ctx, "ghcp", "commit",
		"-r", fmt.Sprintf("%s/%s", c.param.RepoOwner, c.param.RepoName),
		"-b", branch, "-m", prTitle,
		"-d", fmt.Sprintf("%s,%s", filepath.Join(oldPkgDir, "pkg.yaml"), filepath.Join(oldPkgDir, "registry.yaml")),
		"registry.yaml",
		filepath.Join(pkgDir, "registry.yaml"),
		filepath.Join(pkgDir, "pkg.yaml")); err != nil {
		return fmt.Errorf("create a branch: %w", err)
	}
	if _, err := c.createPR(ctx, &ParamCreatePR{
		Title:  prTitle,
		Branch: branch,
		Body:   prBody,
	}); err != nil {
		return fmt.Errorf("create a pull request: %w", err)
	}
	return nil
}
