package controller

import (
	"context"
	"encoding/json"
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

func (ctrl *Controller) Update(ctx context.Context, logE *logrus.Entry, param *Param) error {
	cfg := &Config{}
	if err := ctrl.readConfig("config.yaml", cfg); err != nil {
		return err
	}

	// Get data from GHCR
	const reg = "ghcr.io"
	repo, err := ctrl.newRepo(reg, param.GitHubToken)
	if err != nil {
		return fmt.Errorf("create a client for a remote repository: %w", err)
	}

	const tag = "latest"

	if err := pullFiles(ctx, repo, reg, tag); err != nil {
		return err
	}

	// Read data.json
	data := &Data{}
	if err := ctrl.readData("dist/data.json", data); err != nil {
		return err
	}

	pkgM := make(map[string]struct{}, len(data.Packages))
	for _, pkg := range data.Packages {
		pkgM[pkg.Name] = struct{}{}
	}

	data.Packages = append(data.Packages, &Package{
		Name:            "cli/cli",
		LastCheckedTime: "2023-05-22T05:52:00Z",
	})
	if err := ctrl.writeData("dist/data.json", data); err != nil {
		return err
	}

	// list pkg.yaml
	pkgPaths := []string{}
	if err := fs.WalkDir(afero.NewIOFS(ctrl.fs), "pkgs", func(p string, dirEntry fs.DirEntry, e error) error {
		if dirEntry.Name() != "pkg.yaml" {
			return nil
		}
		pkgPaths = append(pkgPaths, p)
		return nil
	}); err != nil {
		return fmt.Errorf("search pkg.yaml: %w", err)
	}
	for _, pkgPath := range pkgPaths {
		pkgName := strings.TrimSuffix(strings.TrimPrefix(pkgPath, "pkgs/"), "/pkg.yaml")
		if _, ok := pkgM[pkgName]; ok {
			continue
		}
		data.Packages = append(data.Packages, &Package{
			Name: pkgName,
		})
	}

	cnt := 0
	var idx int
	for i, pkg := range data.Packages {
		if cnt == 10 {
			idx = i
			break
		}
		logE := logE.WithField("pkg_name", pkg.Name)
		pkgPath := filepath.Join("pkgs", pkg.Name, "pkg.yaml")
		pattern, err := regexp.Compile(fmt.Sprintf(`- name: %s@(.*)`, pkg.Name))
		if err != nil {
			return fmt.Errorf("compile a regular expression: %w", err)
		}
		body, err := afero.ReadFile(ctrl.fs, pkgPath)
		if err != nil {
			return fmt.Errorf("read pkg.yaml: %w", err)
		}
		bodyS := string(body)
		arr := pattern.FindStringSubmatch(bodyS)
		if len(arr) == 0 {
			continue
		}
		currentVersion := arr[1]
		logE = logE.WithField("current_version", currentVersion)
		repoOwner, a, found := strings.Cut(pkg.Name, "/")
		if !found {
			continue
		}
		if repoOwner == "golang.org" {
			// TODO
			continue
		}
		if repoOwner == "crates.io" {
			// TODO
			continue
		}
		if strings.Contains(repoOwner, ".") {
			// TODO
			continue
		}
		repoName, _, _ := strings.Cut(a, "/")

		cnt++

		// TODO github_tag
		release, _, err := ctrl.repo.GetLatestRelease(ctx, repoOwner, repoName)
		if err != nil {
			logE.WithError(err).Error("get a latest release")
			continue
		}
		tagName := release.GetTagName()
		if tagName == currentVersion {
			logE.Info("already up-to-date")
			continue
		}
		bodyS = strings.Replace(bodyS, "@"+currentVersion, "@"+tagName, 1)
		f, err := ctrl.fs.Create(pkgPath)
		if err != nil {
			logE.WithError(err).Error("open pkg.yaml to update")
			continue
		}
		defer f.Close()
		if _, err := f.WriteString(bodyS); err != nil {
			logE.WithError(err).Error("write pkg.yaml")
			continue
		}

		prTitle := fmt.Sprintf("chore: update %s %s to %s", pkg.Name, currentVersion, tagName)

		branch := fmt.Sprintf("aqua-registry-updater-%s-%s", pkg.Name, tagName)
		if err := ctrl.exec(ctx, "ghcp", "commit", "-r", param.Repo, "-b", branch, "-m", prTitle, pkgPath); err != nil {
			logE.WithError(err).Error("push a commit")
			continue
		}
		if err := ctrl.createPR(ctx, repoOwner, repoName, &ParamCreatePR{
			Release:        release,
			CurrentVersion: currentVersion,
			Title:          prTitle,
			Branch:         branch,
		}); err != nil {
			logE.WithError(err).Error("create a pull request")
			continue
		}
	}

	// update data and upload it to GHCR
	data.Packages = append(data.Packages[idx:], data.Packages[:idx]...)

	if err := pushFiles(ctx, repo, tag); err != nil {
		return err
	}

	// extract package and version
	// extract target packages
	// get the latest version
	// update the version
	// create pull requests
	return nil
}

type ParamCreatePR struct {
	Release        *github.RepositoryRelease
	CurrentVersion string
	Title          string
	Branch         string
}

func (ctrl *Controller) createPR(ctx context.Context, repoOwner, repoName string, param *ParamCreatePR) error {
	release := param.Release
	tagName := release.GetTagName()
	prBody := fmt.Sprintf(
		`[%s](%s) [compare](https://github.com/%s/%s/compare/%s...%s)`,
		tagName,
		release.GetHTMLURL(),
		repoOwner, repoName,
		param.CurrentVersion, tagName,
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
	return runner.Run(ctx, cmd)
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
	Repo        string
}

type Config struct{}

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
		fmt.Printf("file descriptor for %s: %v\n", name, fileDescriptor)
	}

	// 2. Pack the files and tag the packed manifest
	artifactType := "example/files"
	manifestDescriptor, err := oras.Pack(ctx, fs, artifactType, fileDescriptors, oras.PackOptions{
		PackImageManifest: true,
	})
	if err != nil {
		return fmt.Errorf("pack files: %w", err)
	}
	fmt.Println("manifest descriptor:", manifestDescriptor)

	if err := fs.Tag(ctx, manifestDescriptor, tag); err != nil {
		return fmt.Errorf("tag the packed manifest: %w", err)
	}

	if _, err := oras.Copy(ctx, fs, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy from the file store to the remote repository: %w", err)
	}
	return nil
}

func pullFiles(ctx context.Context, repo *remote.Repository, reg, tag string) error {
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
