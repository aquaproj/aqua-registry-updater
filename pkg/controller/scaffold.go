package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/aquaproj/aqua/v2/pkg/config/registry"
	genrg "github.com/aquaproj/registry-tool/pkg/generate-registry"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/go-exec/goexec"
	"gopkg.in/yaml.v3"
)

func (c *Controller) checkBranch(ctx context.Context, branch string) (f bool, e error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/aquaproj/aqua-registry/tree/"+branch, nil)
	if err != nil {
		return false, fmt.Errorf("create a http request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("send a http request: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (c *Controller) scaffold(ctx context.Context, logger *slog.Logger, pkg *Package, cfg *Config) (f bool, e error) { //nolint:cyclop,funlen
	branch := "aqua-registry-updater-scaffold-" + pkg.Name
	if ok, err := c.checkBranch(ctx, branch); err != nil {
		return false, fmt.Errorf("check a branch: %w", err)
	} else if ok {
		return true, nil
	}

	pkgPath := filepath.Join("pkgs", pkg.Name, "pkg.yaml")
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
	logger.Info("re-scaffolding")
	if err := c.fs.Remove(pkgPath); err != nil {
		return false, fmt.Errorf("remove pkg.yaml: %w", err)
	}
	stat, err := c.fs.Stat(registryPath)
	if err != nil {
		return false, fmt.Errorf("stat registry.yaml: %w", err)
	}
	registryFile, err := c.fs.OpenFile(registryPath, os.O_RDWR|os.O_TRUNC, stat.Mode())
	if err != nil {
		return false, fmt.Errorf("open registry.yaml: %w", err)
	}
	defer registryFile.Close()
	if _, err := registryFile.WriteString("# yaml-language-server: $schema=https://raw.githubusercontent.com/aquaproj/aqua/main/json-schema/registry.json\n"); err != nil {
		return false, fmt.Errorf("write yaml-language-server to registry.yaml: %w", err)
	}
	cmd := goexec.Command(ctx, "aqua", "gr", "--out-testdata", pkgPath, pkg.Name)
	cmd.Stdout = registryFile
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("run aqua gr %s: %w", pkg.Name, err)
	}
	if err := genrg.GenerateRegistry(); err != nil {
		return false, fmt.Errorf("update registry.yaml: %w", err)
	}
	if err := c.createScaffoldPR(ctx, pkg.Name, pkgInfo, cfg, branch); err != nil {
		return false, fmt.Errorf("create a pull request: %w", err)
	}
	return true, nil
}

func (c *Controller) createScaffoldPR(ctx context.Context, pkgName string, pkgInfo *registry.PackageInfo, cfg *Config, branch string) error {
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
