package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	genrgst "github.com/aquaproj/aqua/v2/pkg/controller/generate-registry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/go-timeout/timeout"
	"github.com/suzuki-shunsuke/logrus-error/logerr"
)

func (c *Controller) Update(ctx context.Context, logE *logrus.Entry, param *Param) error { //nolint:funlen,cyclop
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

	logE.Info("pulling data from the container registry")
	if err := pullFiles(ctx, repo, tag); err != nil {
		return err
	}

	data := &Data{}
	if err := c.readData("data.json", data); err != nil {
		return err
	}

	logE.WithField("num_of_packages", len(data.Packages)).Info("read data.json")
	pkgM := make(map[string]struct{}, len(data.Packages))
	for _, pkg := range data.Packages {
		pkgM[pkg.Name] = struct{}{}
	}

	logE.Info("searching pkg.yaml from pkgs")
	pkgPaths, err := c.listPkgYAML()
	if err != nil {
		return fmt.Errorf("search pkg.yaml: %w", err)
	}

	ignorePkgsM := make(map[string]struct{}, len(cfg.IgnorePackages))
	for _, pkg := range cfg.IgnorePackages {
		ignorePkgsM[pkg] = struct{}{}
	}

	logE.WithField("num_of_pkgs", len(pkgPaths)).Info("search pkg.yaml from pkgs")
	for _, pkgPath := range pkgPaths {
		pkgName := strings.TrimSuffix(strings.TrimPrefix(pkgPath, "pkgs/"), "/pkg.yaml")
		if _, ok := pkgM[pkgName]; ok {
			continue
		}
		// Append new packages in the end of the package list
		data.Packages = append(data.Packages, &Package{
			Name: pkgName,
		})
	}

	var idx int
	defer func() { //nolint:contextcheck
		data.Packages = append(data.Packages[idx:], data.Packages[:idx]...)
		if err := c.writeData("data.json", data); err != nil {
			logerr.WithError(logE, err).Error("update data.json")
			return
		}
		logE.Info("pushing data.json to the container registry")
		if err := pushFiles(context.Background(), repo, tag); err != nil {
			logerr.WithError(logE, err).Error("push data.json to the container registry")
		}
	}()
	cnt := 0
	for i, pkg := range data.Packages {
		idx = i
		if cnt == cfg.Limit { // Limitation to avoid GitHub API rate limiting
			break
		}
		if _, ok := ignorePkgsM[pkg.Name]; ok {
			continue
		}
		logE := logE.WithField("pkg_name", pkg.Name)
		logE.Info("handling a package")
		incremented, err := c.handlePackage(ctx, logE, pkg, cfg)
		if err != nil {
			logerr.WithError(logE, err).Error("handle a package")
		}
		if incremented {
			cnt++
		}
	}

	return nil
}

func (c *Controller) listPkgYAML() ([]string, error) {
	pkgPaths := []string{}
	if err := fs.WalkDir(afero.NewIOFS(c.fs), "pkgs", func(p string, dirEntry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if dirEntry.Name() != "pkg.yaml" {
			return nil
		}
		pkgPaths = append(pkgPaths, p)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("search pkg.yaml: %w", err)
	}
	return pkgPaths, nil
}

func (c *Controller) handlePackage(ctx context.Context, logE *logrus.Entry, pkg *Package, cfg *Config) (bool, error) { //nolint:cyclop,funlen
	redirected, err := c.fixRedirect(ctx, logE, pkg, cfg)
	if err != nil {
		return false, err
	}
	if redirected {
		return true, nil
	}

	pkgPath := filepath.Join("pkgs", pkg.Name, "pkg.yaml")
	body, err := afero.ReadFile(c.fs, pkgPath)
	if err != nil {
		return false, fmt.Errorf("read pkg.yaml: %w", err)
	}
	bodyS := string(body)

	currentVersion, err := c.getCurrentVersion(pkg.Name, bodyS)
	if err != nil {
		return false, fmt.Errorf("get the current version: %w", err)
	}

	repoOwner, a, found := strings.Cut(pkg.Name, "/")
	if !found {
		return false, errors.New("pkg name doesn't have /")
	}

	repoName, _, _ := strings.Cut(a, "/")

	newVersion, err := c.updatePkgYAML(ctx, pkg.Name, pkgPath, bodyS)
	if err != nil {
		return true, fmt.Errorf("update pkg.yaml: %w", err)
	}
	if newVersion == "" {
		return true, nil
	}

	ignoreVersionMap := map[string]struct{}{
		"latest": {},
		"edge":   {},
		"stable": {},
	}

	if _, ok := ignoreVersionMap[newVersion]; ok {
		return true, nil
	}

	automerged, err := compareVersion(currentVersion, newVersion)
	if err != nil {
		logerr.WithError(logE, err).Warn("compare version")
	} else if !automerged {
		logerr.WithError(logE, err).Warn("ignore the change")
		return true, nil
	}

	paramTemplates := &ParamTemplates{
		PackageName:    pkg.Name,
		RepoOwner:      repoOwner,
		RepoName:       repoName,
		NewVersion:     newVersion,
		CurrentVersion: currentVersion,
		CompareURL:     fmt.Sprintf(`https://github.com/%s/%s/compare/%s...%s`, repoOwner, repoName, currentVersion, newVersion),
		ReleaseURL:     fmt.Sprintf(`https://github.com/%s/%s/releases/tag/%s`, repoOwner, repoName, newVersion),
	}

	if strings.Contains(repoOwner, ".") {
		paramTemplates.RepoOwner = ""
		paramTemplates.RepoName = ""
		paramTemplates.CompareURL = ""
		paramTemplates.ReleaseURL = ""
	}

	prTitle, err := renderTemplate(cfg.compiledTemplates.PRTitle, paramTemplates)
	if err != nil {
		return true, fmt.Errorf("render a template pr_title: %w", err)
	}

	prBody, err := renderTemplate(cfg.compiledTemplates.PRBody, paramTemplates)
	if err != nil {
		return true, fmt.Errorf("render a template pr_body: %w", err)
	}

	branch := fmt.Sprintf("aqua-registry-updater-%s-%s", pkg.Name, newVersion)
	if err := c.exec(ctx, "ghcp", "commit", "-r", fmt.Sprintf("%s/%s", c.param.RepoOwner, c.param.RepoName), "-b", branch, "-m", prTitle, pkgPath); err != nil {
		return true, fmt.Errorf("create a branch: %w", err)
	}
	prNumber, err := c.createPR(ctx, &ParamCreatePR{
		NewVersion:     newVersion,
		CurrentVersion: currentVersion,
		Title:          prTitle,
		Branch:         branch,
		Body:           prBody,
	})
	if err != nil {
		return true, fmt.Errorf("create a pull request: %w", err)
	}

	if automerged {
		if err := c.exec(ctx, "gh", "-R", fmt.Sprintf("%s/%s", c.param.RepoOwner, c.param.RepoName), "pr", "merge", "-s", "--auto", strconv.Itoa(prNumber)); err != nil {
			return true, fmt.Errorf("enable auto-merge: %w", err)
		}
	}
	return true, nil
}

func compareVersion(currentVersion, newVersion string) (bool, error) {
	cv, cvPrefix, err := genrgst.GetVersionAndPrefix(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse the current version: %w", err)
	}
	nv, nvPrefix, err := genrgst.GetVersionAndPrefix(newVersion)
	if err != nil {
		return false, fmt.Errorf("parse the new version: %w", err)
	}
	if cvPrefix != nvPrefix {
		return false, nil
	}
	return nv.GreaterThan(cv), nil
}

func (c *Controller) getCurrentVersion(pkgName, content string) (string, error) {
	pattern, err := regexp.Compile(fmt.Sprintf(`- name: %s@(.*)`, pkgName))
	if err != nil {
		return "", fmt.Errorf("compile a regular expression: %w", err)
	}
	arr := pattern.FindStringSubmatch(content)
	if len(arr) == 0 {
		return "", errors.New("no match")
	}
	return arr[1], nil
}

type ParamUpdatePkgYAML struct {
	OriginalContent string
}

func (c *Controller) updatePkgYAML(ctx context.Context, pkgName, pkgPath, content string) (string, error) {
	newLine, err := c.aquaGenerate(ctx, pkgName)
	if err != nil {
		return "", err
	}
	idx := strings.Index(newLine, "@")
	if idx == -1 {
		return "", nil
	}
	newVersion := newLine[idx+1:]
	lines := strings.Split(content, "\n")
	if len(lines) < 2 { //nolint:mnd
		return "", nil
	}
	if strings.TrimSpace(lines[1]) == strings.TrimSpace(newLine) {
		return "", nil
	}
	if strings.HasPrefix(lines[1], " ") {
		lines[1] = "  " + newLine
	} else {
		lines[1] = newLine
	}
	f, err := c.fs.Create(pkgPath)
	if err != nil {
		return "", fmt.Errorf("open pkg.yaml to update: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n")); err != nil {
		return "", fmt.Errorf("write pkg.yaml: %w", err)
	}
	return newVersion, nil
}

func (c *Controller) aquaGenerate(ctx context.Context, pkgName string) (string, error) {
	cmd := exec.Command("aqua", "g", pkgName)
	buf := &bytes.Buffer{}
	cmd.Stdout = buf
	cmd.Stderr = c.stderr
	if err := timeout.NewRunner(0).Run(ctx, cmd); err != nil {
		return "", err //nolint:wrapcheck
	}
	return strings.TrimSpace(buf.String()), nil
}

func (c *Controller) exec(ctx context.Context, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Stdout = c.stdout
	cmd.Stderr = c.stderr
	runner := timeout.NewRunner(0)
	return runner.Run(ctx, cmd) //nolint:wrapcheck
}

type Param struct {
	GitHubToken string
}
