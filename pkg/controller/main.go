package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/google/go-github/v55/github"
	"github.com/hashicorp/go-version"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/go-timeout/timeout"
	"github.com/suzuki-shunsuke/logrus-error/logerr"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func (ctrl *Controller) Update(ctx context.Context, logE *logrus.Entry, param *Param) error { //nolint:funlen,cyclop
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

	logE.Info("pulling data from the container registry")
	if err := pullFiles(ctx, repo, tag); err != nil {
		return err
	}

	data := &Data{}
	if err := ctrl.readData("data.json", data); err != nil {
		return err
	}

	logE.WithField("num_of_packages", len(data.Packages)).Info("read data.json")
	pkgM := make(map[string]struct{}, len(data.Packages))
	for _, pkg := range data.Packages {
		pkgM[pkg.Name] = struct{}{}
	}

	logE.Info("searching pkg.yaml from pkgs")
	pkgPaths, err := ctrl.listPkgYAML()
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
	defer func() {
		data.Packages = append(data.Packages[idx:], data.Packages[:idx]...)
		if err := ctrl.writeData("data.json", data); err != nil {
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
		incremented, err := ctrl.handlePackage(ctx, logE, pkg, cfg)
		if err != nil {
			logerr.WithError(logE, err).Error("handle a package")
		}
		if incremented {
			cnt++
		}
	}

	return nil
}

func (cfg *Config) SetDefault(repo string) error { //nolint:cyclop
	if cfg.Limit == 0 {
		cfg.Limit = 50
	}
	if cfg.ContainerRegistry == nil {
		return errors.New("container_registry is required")
	}
	if cfg.ContainerRegistry.Auth == nil {
		return errors.New("container_registry.auth is required")
	}
	if cfg.ContainerRegistry.Registry == "" {
		cfg.ContainerRegistry.Registry = "ghcr.io"
	}
	if cfg.ContainerRegistry.Repository == "" {
		cfg.ContainerRegistry.Repository = repo
	}
	if cfg.ContainerRegistry.Auth.Username == "" {
		return errors.New("container_registry.auth.username is required")
	}
	if cfg.Templates == nil {
		cfg.Templates = &Templates{}
	}
	if cfg.Templates.PRTitle == "" {
		cfg.Templates.PRTitle = "chore: update {{.PackageName}} {{.CurrentVersion}} to {{.NewVersion}}"
	}
	if cfg.Templates.PRBody == "" {
		cfg.Templates.PRBody = `[{{.NewVersion}}]({{.ReleaseURL}}) [compare]({{.CompareURL}})

This pull request was created by [aqua-registry-updater](https://github.com/aquaproj/aqua-registry-updater).`
	}
	cfg.compiledTemplates = &CompiledTemplates{}

	prTitle, err := compileTemplate(cfg.Templates.PRTitle)
	if err != nil {
		return fmt.Errorf("compile a template pr_title: %w", err)
	}
	cfg.compiledTemplates.PRTitle = prTitle

	prBody, err := compileTemplate(cfg.Templates.PRBody)
	if err != nil {
		return fmt.Errorf("compile a template pr_body: %w", err)
	}
	cfg.compiledTemplates.PRBody = prBody

	return nil
}

func (ctrl *Controller) listPkgYAML() ([]string, error) {
	pkgPaths := []string{}
	if err := fs.WalkDir(afero.NewIOFS(ctrl.fs), "pkgs", func(p string, dirEntry fs.DirEntry, e error) error {
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

func (ctrl *Controller) handlePackage(ctx context.Context, logE *logrus.Entry, pkg *Package, cfg *Config) (bool, error) { //nolint:cyclop,funlen
	pkgPath := filepath.Join("pkgs", pkg.Name, "pkg.yaml")
	body, err := afero.ReadFile(ctrl.fs, pkgPath)
	if err != nil {
		return false, fmt.Errorf("read pkg.yaml: %w", err)
	}
	bodyS := string(body)

	currentVersion, err := ctrl.getCurrentVersion(pkg.Name, bodyS)
	if err != nil {
		return false, fmt.Errorf("get the current version: %w", err)
	}

	repoOwner, a, found := strings.Cut(pkg.Name, "/")
	if !found {
		return false, errors.New("pkg name doesn't have /")
	}
	if repoOwner == "golang.org" {
		// TODO
		return false, nil
	}
	if strings.Contains(repoOwner, ".") {
		// TODO
		return false, nil
	}
	repoName, _, _ := strings.Cut(a, "/")

	newVersion, err := ctrl.updatePkgYAML(ctx, pkg.Name, pkgPath, bodyS)
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

	prTitle, err := renderTemplate(cfg.compiledTemplates.PRTitle, paramTemplates)
	if err != nil {
		return true, fmt.Errorf("render a template pr_title: %w", err)
	}

	prBody, err := renderTemplate(cfg.compiledTemplates.PRBody, paramTemplates)
	if err != nil {
		return true, fmt.Errorf("render a template pr_body: %w", err)
	}

	branch := fmt.Sprintf("aqua-registry-updater-%s-%s", pkg.Name, newVersion)
	if err := ctrl.exec(ctx, "ghcp", "commit", "-r", fmt.Sprintf("%s/%s", ctrl.param.RepoOwner, ctrl.param.RepoName), "-b", branch, "-m", prTitle, pkgPath); err != nil {
		return true, fmt.Errorf("create a branch: %w", err)
	}
	prNumber, err := ctrl.createPR(ctx, &ParamCreatePR{
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
		if err := ctrl.exec(ctx, "gh", "-R", fmt.Sprintf("%s/%s", ctrl.param.RepoOwner, ctrl.param.RepoName), "pr", "merge", "-s", "--auto", strconv.Itoa(prNumber)); err != nil {
			return true, fmt.Errorf("enable auto-merge: %w", err)
		}
	}
	return true, nil
}

func compareVersion(currentVersion, newVersion string) (bool, error) {
	c, err := version.NewVersion(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse the current version: %w", err)
	}
	n, err := version.NewVersion(newVersion)
	if err != nil {
		return false, fmt.Errorf("parse the new version: %w", err)
	}
	return n.GreaterThan(c), nil
}

func (ctrl *Controller) getCurrentVersion(pkgName, content string) (string, error) {
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

func (ctrl *Controller) updatePkgYAML(ctx context.Context, pkgName, pkgPath, content string) (string, error) {
	newLine, err := ctrl.aquaGenerate(ctx, pkgName)
	if err != nil {
		return "", err
	}
	idx := strings.Index(newLine, "@")
	if idx == -1 {
		return "", nil
	}
	newVersion := newLine[idx+1:]
	lines := strings.Split(content, "\n")
	if len(lines) < 2 { //nolint:gomnd
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
	f, err := ctrl.fs.Create(pkgPath)
	if err != nil {
		return "", fmt.Errorf("open pkg.yaml to update: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n")); err != nil {
		return "", fmt.Errorf("write pkg.yaml: %w", err)
	}
	return newVersion, nil
}

func (ctrl *Controller) aquaGenerate(ctx context.Context, pkgName string) (string, error) {
	cmd := exec.Command("aqua", "g", pkgName)
	buf := &bytes.Buffer{}
	cmd.Stdout = buf
	cmd.Stderr = ctrl.stderr
	if err := timeout.NewRunner(0).Run(ctx, cmd); err != nil {
		return "", err //nolint:wrapcheck
	}
	return strings.TrimSpace(buf.String()), nil
}

type ParamCreatePR struct {
	NewVersion     string
	CurrentVersion string
	Title          string
	Branch         string
	Body           string
}

func (ctrl *Controller) createPR(ctx context.Context, param *ParamCreatePR) (int, error) {
	pr, _, err := ctrl.pull.Create(ctx, ctrl.param.RepoOwner, ctrl.param.RepoName, &github.NewPullRequest{
		Head:  github.String(param.Branch),
		Base:  github.String("main"),
		Title: github.String(param.Title),
		Body:  github.String(param.Body),
	})
	if err != nil {
		return 0, fmt.Errorf("create a pull request: %w", err)
	}
	return pr.GetNumber(), nil
}

func (ctrl *Controller) exec(ctx context.Context, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Stdout = ctrl.stdout
	cmd.Stderr = ctrl.stderr
	runner := timeout.NewRunner(0)
	return runner.Run(ctx, cmd) //nolint:wrapcheck
}

func NewGitHub(ctx context.Context, token string) *github.Client {
	return github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))
}

func (ctrl *Controller) newRepo(reg *ContainerRegistry, token string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reg.Registry + "/" + reg.Repository)
	if err != nil {
		return nil, fmt.Errorf("create a client for a remote repository: %w", err)
	}
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.DefaultCache,
		Credential: auth.StaticCredential(reg.Registry, auth.Credential{
			Username: reg.Auth.Username,
			Password: token,
		}),
	}
	return repo, nil
}

type ParamNew struct {
	RepoOwner string
	RepoName  string
}

type Controller struct {
	fs afero.Fs
	// repo   RepositoriesService
	pull   PullRequestsService
	stdout io.Writer
	stderr io.Writer
	param  *ParamNew
}

type PullRequestsService interface {
	Create(ctx context.Context, owner, repo string, pull *github.NewPullRequest) (*github.PullRequest, *github.Response, error)
}

func New(fs afero.Fs, param *ParamNew, pull PullRequestsService) *Controller {
	return &Controller{
		fs:     fs,
		pull:   pull,
		stdout: os.Stdout,
		stderr: os.Stderr,
		param:  param,
	}
}

type Param struct {
	GitHubToken string
}

type Config struct {
	Limit             int
	ContainerRegistry *ContainerRegistry `yaml:"container_registry"`
	IgnorePackages    []string           `yaml:"ignore_packages"`
	Templates         *Templates
	compiledTemplates *CompiledTemplates
}

type Templates struct {
	PRTitle string `yaml:"pr_title"`
	PRBody  string `yaml:"pr_body"`
}

type CompiledTemplates struct {
	PRTitle *template.Template
	PRBody  *template.Template
}

type ParamTemplates struct {
	PackageName    string
	RepoOwner      string
	RepoName       string
	CompareURL     string
	ReleaseURL     string
	NewVersion     string
	CurrentVersion string
}

func compileTemplate(s string) (*template.Template, error) {
	return template.New("_").Parse(s) //nolint:wrapcheck
}

func renderTemplate(tpl *template.Template, param *ParamTemplates) (string, error) {
	b := &bytes.Buffer{}
	if err := tpl.Execute(b, param); err != nil {
		return "", err //nolint:wrapcheck
	}
	return b.String(), nil
}

type ContainerRegistry struct {
	Registry   string
	Repository string
	Auth       *ContainerRegistryAuth
}

type ContainerRegistryAuth struct {
	Username string
}

type Data struct {
	Packages []*Package `json:"packages"`
}

type Package struct {
	Name string `json:"name"`
}

func (ctrl *Controller) writeData(path string, data *Data) error {
	f, err := ctrl.fs.Create(path)
	if err != nil {
		return fmt.Errorf("create a data file: %w", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("write data to a file: %w", err)
	}
	return nil
}

func (ctrl *Controller) readData(path string, data *Data) error {
	f, err := ctrl.fs.Open(path)
	if err != nil {
		return fmt.Errorf("open a data file: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(data); err != nil {
		return fmt.Errorf("read a data file as JSON: %w", err)
	}
	return nil
}

func (ctrl *Controller) readConfig(path string, cfg *Config) error {
	f, err := ctrl.fs.Open(path)
	if err != nil {
		return fmt.Errorf("open a configuration file: %w", err)
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("read a configuration file as YAML: %w", err)
	}
	return nil
}

func pushFiles(ctx context.Context, repo *remote.Repository, tag string) error {
	fs, err := file.New("")
	if err != nil {
		return fmt.Errorf("create a file store: %w", err)
	}
	defer fs.Close()
	mediaType := "example/file" // "application/vnd.unknown.config.v1+json"
	fileNames := []string{"data.json"}
	fileDescriptors := make([]v1.Descriptor, 0, len(fileNames))
	for _, name := range fileNames {
		fileDescriptor, err := fs.Add(ctx, name, mediaType, "")
		if err != nil {
			return fmt.Errorf("add a file to the file store: %w", err)
		}
		fileDescriptors = append(fileDescriptors, fileDescriptor)
	}

	// 2. Pack the files and tag the packed manifest
	artifactType := "example/files"
	manifestDescriptor, err := oras.Pack(ctx, fs, artifactType, fileDescriptors, oras.PackOptions{
		PackImageManifest: true,
	})
	if err != nil {
		return fmt.Errorf("pack files: %w", err)
	}

	if err := fs.Tag(ctx, manifestDescriptor, tag); err != nil {
		return fmt.Errorf("tag the packed manifest: %w", err)
	}

	if _, err := oras.Copy(ctx, fs, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy from the file store to the remote repository: %w", err)
	}
	return nil
}

func pullFiles(ctx context.Context, repo *remote.Repository, tag string) error {
	// 0. Create a file store
	fs, err := file.New("")
	if err != nil {
		return fmt.Errorf("create a file store: %w", err)
	}
	defer fs.Close()

	if _, err := oras.Copy(ctx, repo, tag, fs, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy from the remote repository to the file store: %w", err)
	}
	return nil
}
