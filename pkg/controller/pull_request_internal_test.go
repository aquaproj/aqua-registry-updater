package controller

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-github/v89/github"
	"github.com/spf13/afero"
)

const (
	testInvalidVersion = "branch"
	testJena610        = "jena-6.1.0"
)

func Test_isCurrentVersionSameOrNewer(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		current  string
		proposed string
		closePR  bool
		wantErr  bool
	}{
		"same":                    {testJena610, testJena610, true, false},
		"newer":                   {"jena-6.2.0", testJena610, true, false},
		"older":                   {"jena-6.0.0", testJena610, false, false},
		"distinct build metadata": {"v1.36.2+k3s1", "v1.36.2+k3s2", false, false},
		"distinct normalized tag": {"v1.2", "v1.2.0", false, false},
		"different prefix":        {"edge-v2.0.0", "stable-v2.0.0", false, false},
		"same invalid version":    {testInvalidVersion, testInvalidVersion, false, true},
		"invalid current":         {testInvalidVersion, "v1.0.0", false, true},
		"invalid proposed":        {"v1.0.0", testInvalidVersion, false, true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			closePR, err := isCurrentVersionSameOrNewer(test.current, test.proposed)
			if closePR != test.closePR || (err != nil) != test.wantErr {
				t.Fatalf("got (%v, %v), want (%v, error=%v)", closePR, err, test.closePR, test.wantErr)
			}
		})
	}
}

func TestController_closeSupersededUpdatePRs(t *testing.T) {
	t.Parallel()
	ctrl, pull, paths := newTestController(t)
	if err := ctrl.closeSupersededUpdatePRs(
		context.Background(), slog.New(slog.DiscardHandler), paths, nil,
	); err != nil {
		t.Fatal(err)
	}
	if len(pull.editedNumbers) != 3 ||
		pull.editedNumbers[0] != 53169 ||
		pull.editedNumbers[1] != 3 ||
		pull.editedNumbers[2] != 4 {
		t.Fatalf("wanted pull requests [53169 3 4] to be closed, got %v", pull.editedNumbers)
	}
	if len(pull.listFilesNumbers) != 3 ||
		pull.listFilesNumbers[0] != 53169 ||
		pull.listFilesNumbers[1] != 3 ||
		pull.listFilesNumbers[2] != 4 {
		t.Fatalf("wanted files of pull requests [53169 3 4] to be listed, got %v", pull.listFilesNumbers)
	}
}

func TestController_closeSupersededUpdatePRs_targeted(t *testing.T) {
	t.Parallel()
	ctrl, pull, paths := newTestController(t)
	if err := ctrl.closeSupersededUpdatePRs(
		context.Background(), slog.New(slog.DiscardHandler), paths, []string{"owner/tool"},
	); err != nil {
		t.Fatal(err)
	}
	if len(pull.editedNumbers) != 1 || pull.editedNumbers[0] != 3 {
		t.Fatalf("wanted only pull request 3 to be closed, got %v", pull.editedNumbers)
	}
}

type fakePullRequestsService struct {
	editedNumbers    []int
	listFilesNumbers []int
}

func (*fakePullRequestsService) Create(context.Context, string, string, *github.NewPullRequest) (*github.PullRequest, *github.Response, error) {
	return nil, nil, errors.New("not implemented")
}

func (f *fakePullRequestsService) Edit(_ context.Context, _, _ string, number int, pull *github.PullRequest) (*github.PullRequest, *github.Response, error) {
	if pull.GetState() != "closed" {
		return nil, nil, errors.New("pull request state must be closed")
	}
	f.editedNumbers = append(f.editedNumbers, number)
	return pull, &github.Response{}, nil
}

func (*fakePullRequestsService) List(_ context.Context, _, _ string, opts *github.PullRequestListOptions) ([]*github.PullRequest, *github.Response, error) {
	if opts.State != "open" || opts.Base != defaultBranchName || opts.PerPage != 100 {
		return nil, nil, errors.New("unexpected list options")
	}
	if opts.Page == 0 {
		return []*github.PullRequest{
			newUpdatePullRequest(53169, updatePRAuthor, "apache/jena", testJena610),
			newUpdatePullRequest(1, "octocat", "apache/jena", testJena610),
		}, &github.Response{NextPage: 2}, nil
	}
	return []*github.PullRequest{
		newUpdatePullRequest(2, updatePRAuthor, "apache/jena", "jena-6.2.0"),
		newUpdatePullRequest(3, updatePRAuthor, "owner/tool-extra", "v1.0.0"),
		newUpdatePullRequest(4, updatePRAuthor, "apache/jena", "jena-6.0.0"),
	}, &github.Response{}, nil
}

func (f *fakePullRequestsService) ListFiles(_ context.Context, _, _ string, number int, _ *github.ListOptions) ([]*github.CommitFile, *github.Response, error) {
	f.listFilesNumbers = append(f.listFilesNumbers, number)
	filename := "pkgs/apache/jena/pkg.yaml"
	if number == 3 {
		filename = "pkgs/owner/tool/pkg.yaml"
	}
	return []*github.CommitFile{
		{Filename: new(filename)},
	}, &github.Response{}, nil
}

func newUpdatePullRequest(number int, author, pkg, version string) *github.PullRequest {
	return &github.PullRequest{
		Number: new(number),
		User:   &github.User{Login: new(author)},
		Head: &github.PullRequestBranch{
			Ref:  new(updateBranchPrefix + pkg + "-" + version),
			Repo: &github.Repository{FullName: new("aquaproj/aqua-registry")},
		},
	}
}

func writePkgYAML(t *testing.T, fs afero.Fs, path, pkg, version string) {
	t.Helper()
	if err := fs.MkdirAll(strings.TrimSuffix(path, "/pkg.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "packages:\n  - name: " + pkg + "@" + version + "\n"
	if err := afero.WriteFile(fs, path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestController(t *testing.T) (*Controller, *fakePullRequestsService, []string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	paths := []string{
		"pkgs/apache/jena/pkg.yaml",
		"pkgs/owner/tool/pkg.yaml",
		"pkgs/owner/tool-extra/pkg.yaml",
	}
	writePkgYAML(t, fs, paths[0], "apache/jena", testJena610)
	writePkgYAML(t, fs, paths[1], "owner/tool", "extra-v2.0.0")
	writePkgYAML(t, fs, paths[2], "owner/tool-extra", "v0.9.0")
	pull := &fakePullRequestsService{}
	return &Controller{
		fs:    fs,
		pull:  pull,
		param: &ParamNew{RepoOwner: "aquaproj", RepoName: "aqua-registry"},
	}, pull, paths
}
