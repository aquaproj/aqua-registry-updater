package controller

import (
	"context"
	"fmt"

	"github.com/google/go-github/v67/github"
	"golang.org/x/oauth2"
)

func NewGitHub(ctx context.Context, token string) *github.Client {
	return github.NewClient(oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))
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
