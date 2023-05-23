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
	"strings"

	"github.com/google/go-github/v52/github"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/go-timeout/timeout"
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
	if err := ctrl.readConfig("config.yaml", cfg); err != nil {
		return err
	}
	if cfg.Limit == 0 {
		cfg.Limit = 50
	}

	// Get data from GHCR
	repo, err := ctrl.newRepo("ghcr.io", param.GitHubToken)
	if err != nil {
		return fmt.Errorf("create a client for a remote repository: %w", err)
	}

	const tag = "latest"

	if err := pullFiles(ctx, repo, tag); err != nil {
		return err
	}

	data := &Data{}
	if err := ctrl.readData("dist/data.json", data); err != nil {
		return err
	}

	pkgM := make(map[string]struct{}, len(data.Packages))
	for _, pkg := range data.Packages {
		pkgM[pkg.Name] = struct{}{}
	}

	pkgPaths, err := ctrl.listPkgYAML()
	if err != nil {
		return fmt.Errorf("search pkg.yaml: %w", err)
	}

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

	cnt := 0
	var idx int
	for i, pkg := range data.Packages {
		if cnt == cfg.Limit { // Limitation to avoid GitHub API rate limiting
			idx = i
			break
		}
		logE := logE.WithField("pkg_name", pkg.Name)
		incremented, err := ctrl.handlePackage(ctx, pkg)
		if err != nil {
			logE.WithError(err).Error("handle a package")
		}
		if incremented {
			cnt++
		}
	}

	// update data and upload it to GHCR
	data.Packages = append(data.Packages[idx:], data.Packages[:idx]...)

	if err := ctrl.writeData("dist/data.json", data); err != nil {
		return fmt.Errorf("update data.json: %w", err)
	}

	if err := pushFiles(ctx, repo, tag); err != nil {
		return err
	}

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

func (ctrl *Controller) handlePackage(ctx context.Context, pkg *Package) (bool, error) { //nolint:cyclop
	pkgPath := filepath.Join("pkgs", pkg.Name, "pkg.yaml")
	if err := ctrl.exec(ctx, "git", "checkout", "main"); err != nil {
		return true, fmt.Errorf("git checkout main: %w", err)
	}
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

	prTitle := fmt.Sprintf("chore: update %s %s to %s", pkg.Name, currentVersion, newVersion)
	branch := fmt.Sprintf("aqua-registry-updater-%s-%s", pkg.Name, newVersion)
	if err := ctrl.exec(ctx, "git", "checkout", "-b", branch); err != nil {
		return true, fmt.Errorf("create a branch: %w", err)
	}
	if err := ctrl.exec(ctx, "git", "add", pkgPath); err != nil {
		return true, fmt.Errorf("git add %s: %w", pkgPath, err)
	}
	if err := ctrl.exec(ctx, "git", "commit", "-m", prTitle); err != nil {
		return true, fmt.Errorf(`git commit -m "%s": %w`, prTitle, err)
	}
	if err := ctrl.exec(ctx, "git", "push", "origin", branch); err != nil {
		return true, fmt.Errorf(`git push origin %s: %w`, branch, err)
	}
	if err := ctrl.createPR(ctx, repoOwner, repoName, &ParamCreatePR{
		NewVersion:     newVersion,
		CurrentVersion: currentVersion,
		Title:          prTitle,
		Branch:         branch,
	}); err != nil {
		return true, fmt.Errorf("create a pull request: %w", err)
	}
	return true, nil
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
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
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
}

func (ctrl *Controller) createPR(ctx context.Context, repoOwner, repoName string, param *ParamCreatePR) error {
	newVersion := param.NewVersion
	prBody := fmt.Sprintf(
		`[%s](%s) [compare](https://github.com/%s/%s/compare/%s...%s)`,
		newVersion,
		fmt.Sprintf(`https://github.com/%s/%s/releases/tag/%s`, repoOwner, repoName, newVersion),
		repoOwner, repoName,
		param.CurrentVersion, newVersion,
	)
	if err := ctrl.exec(ctx, "gh", "pr", "create", "--head", param.Branch, "-t", param.Title, "-b", prBody); err != nil {
		return err
	}
	return nil
}

func (ctrl *Controller) exec(ctx context.Context, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.Stdout = ctrl.stdout
	cmd.Stderr = ctrl.stderr
	runner := timeout.NewRunner(0)
	return runner.Run(ctx, cmd) //nolint:wrapcheck
}

type RepositoriesService interface {
	GetLatestRelease(ctx context.Context, owner, repo string) (*github.RepositoryRelease, *github.Response, error)
}

func NewGitHub(ctx context.Context, token string) *github.Client {
	return github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))
}

func (ctrl *Controller) newRepo(reg, token string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reg + "/suzuki-shunsuke/aqua-registry-updater")
	if err != nil {
		return nil, fmt.Errorf("create a client for a remote repository: %w", err)
	}
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Cache:  auth.DefaultCache,
		Credential: auth.StaticCredential(reg, auth.Credential{
			Username: "suzuki-shunsuke",
			Password: token,
			// Password: os.Getenv("GITHUB_TOKEN"),
		}),
	}
	return repo, nil
}

type Controller struct {
	fs     afero.Fs
	repo   RepositoriesService
	stdout io.Writer
	stderr io.Writer
}

func New(fs afero.Fs, repo RepositoriesService) *Controller {
	return &Controller{
		fs:     fs,
		repo:   repo,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

type Param struct {
	GitHubToken string
}

type Config struct {
	Limit int
}

type Data struct {
	Packages []*Package `json:"packages"`
}

type Package struct {
	Name            string `json:"name"`
	LastCheckedTime string `json:"last_checked_time"`
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
	fs, err := file.New("dist")
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
	fs, err := file.New("dist")
	if err != nil {
		return fmt.Errorf("create a file store: %w", err)
	}
	defer fs.Close()

	if _, err := oras.Copy(ctx, repo, tag, fs, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy from the remote repository to the file store: %w", err)
	}
	return nil
}
