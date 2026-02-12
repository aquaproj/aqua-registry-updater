package controller

import (
	"context"
	"io"
	"os"

	"github.com/google/go-github/v82/github"
	"github.com/spf13/afero"
)

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
