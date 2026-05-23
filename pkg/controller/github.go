package controller

import (
	"context"
	"fmt"

	"github.com/google/go-github/v87/github"
	"golang.org/x/oauth2"
)

func NewGitHub(ctx context.Context, token string) (*github.Client, error) {
	v3, err := github.NewClient(github.WithHTTPClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	))))
	if err != nil {
		return nil, fmt.Errorf("create a GitHub client: %w", err)
	}
	return v3, nil
}

type ParamCreatePR struct {
	NewVersion     string
	CurrentVersion string
	Title          string
	Branch         string
	Body           string
}

func (c *Controller) createPR(ctx context.Context, param *ParamCreatePR) (int, error) {
	pr, _, err := c.pull.Create(ctx, c.param.RepoOwner, c.param.RepoName, &github.NewPullRequest{
		Head:  new(param.Branch),
		Base:  new("main"),
		Title: new(param.Title),
		Body:  new(param.Body),
	})
	if err != nil {
		return 0, fmt.Errorf("create a pull request: %w", err)
	}
	return pr.GetNumber(), nil
}
