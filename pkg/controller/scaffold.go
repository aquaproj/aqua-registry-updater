package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/aquaproj/aqua/v2/pkg/config/registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/go-exec/goexec"
	"gopkg.in/yaml.v3"
)

func (c *Controller) scaffold(ctx context.Context, logE *logrus.Entry, pkg *Package, cfg *Config) (f bool, e error) {
	registryPath := filepath.Join("pkgs", pkg.Name, "registry.yaml")
	body, err := afero.ReadFile(c.fs, registryPath)
	if err != nil {
		return false, fmt.Errorf("read registry.yaml: %w", err)
	}
	rCfg := &registry.Config{}
	if err := yaml.Unmarshal(body, rCfg); err != nil {
		return false, fmt.Errorf("unmarshal registry.yaml as YAML: %w", err)
	}
	if len(rCfg.PackageInfos) == 0 {
		return false, errors.New("registry.yaml is empty")
	}
	if len(rCfg.PackageInfos) != 1 {
		return false, errors.New("registry.yaml must have only one package")
	}
	pkgInfo := rCfg.PackageInfos[0]
	if pkgInfo == nil {
		return false, errors.New("package is nil")
	}
	if pkgInfo.Type != "github_release" {
		return false, nil
	}
	if pkgInfo.VersionConstraints == "false" {
		return false, nil
	}
	logE.Info("running cmdx s")
	if err := goexec.Command(ctx, "cmdx", "s", pkg.Name).Run(); err != nil {
		return false, fmt.Errorf("run cmdx s %s: %w", pkg.Name, err)
	}
	if err := c.createScaffoldPR(ctx, pkg.Name, pkgInfo, cfg); err != nil {
		return false, fmt.Errorf("create a pull request: %w", err)
	}
	return true, nil
}

func (c *Controller) createScaffoldPR(ctx context.Context, pkgName string, pkgInfo *registry.PackageInfo, cfg *Config) error {
	paramTemplates := &ParamTemplates{
		PackageName: pkgName,
		RepoOwner:   pkgInfo.RepoOwner,
		RepoName:    pkgInfo.RepoName,
	}

	prTitle, err := renderTemplate(cfg.compiledTemplates.ScaffoldPRTitle, paramTemplates)
	if err != nil {
		return fmt.Errorf("render a template scaffold_pr_title: %w", err)
	}

	prBody, err := renderTemplate(cfg.compiledTemplates.ScaffoldPRBody, paramTemplates)
	if err != nil {
		return fmt.Errorf("render a template scaffold_pr_body: %w", err)
	}

	pkgDir := filepath.Join("pkgs", filepath.FromSlash(pkgName))
	branch := "aqua-registry-updater-scaffold-" + pkgName
	if err := c.exec(ctx, "ghcp", "commit",
		"-r", fmt.Sprintf("%s/%s", c.param.RepoOwner, c.param.RepoName),
		"-b", branch, "-m", prTitle,
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
