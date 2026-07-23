package controller

import (
	"context"
	"io"
	"os"

	"github.com/google/go-github/v89/github"
	"github.com/spf13/afero"
)

type ParamNew struct {
	RepoOwner string
	RepoName  string
}

type Controller struct {
	fs     afero.Fs
	pull   PullRequestsService
	stdout io.Writer
	stderr io.Writer
	param  *ParamNew
}

type PullRequestsService interface {
	Create(ctx context.Context, owner, repo string, pull *github.NewPullRequest) (*github.PullRequest, *github.Response, error)
	Edit(ctx context.Context, owner, repo string, number int, pull *github.PullRequest) (*github.PullRequest, *github.Response, error)
	List(ctx context.Context, owner, repo string, opts *github.PullRequestListOptions) ([]*github.PullRequest, *github.Response, error)
	ListFiles(ctx context.Context, owner, repo string, number int, opts *github.ListOptions) ([]*github.CommitFile, *github.Response, error)
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
