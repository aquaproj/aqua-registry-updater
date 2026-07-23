package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/versiongetter"
	"github.com/google/go-github/v89/github"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

const (
	defaultBranchName  = "main"
	updateBranchPrefix = "aqua-registry-updater-"
	updatePRAuthor     = "aquaproj-aqua-registry[bot]"
)

func (c *Controller) listOpenUpdatePRs(ctx context.Context) ([]*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		State:       "open",
		Base:        defaultBranchName,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	prs := []*github.PullRequest{}
	for {
		page, resp, err := c.pull.List(ctx, c.param.RepoOwner, c.param.RepoName, opts)
		if err != nil {
			return nil, fmt.Errorf("list pull requests: %w", err)
		}
		prs = append(prs, page...)
		if resp.NextPage == 0 {
			return prs, nil
		}
		opts.Page = resp.NextPage
	}
}

func (c *Controller) closeSupersededUpdatePRs(ctx context.Context, logger *slog.Logger, paths, packages []string) error {
	prs, err := c.listOpenUpdatePRs(ctx)
	if err != nil {
		return err
	}
	pkgPaths := make(map[string]string, len(paths))
	for _, path := range paths {
		pkgPaths[packageNameFromPath(path)] = path
	}
	selected := make(map[string]struct{}, len(packages))
	for _, pkgName := range packages {
		selected[pkgName] = struct{}{}
	}
	repo := c.param.RepoOwner + "/" + c.param.RepoName
	for _, pr := range prs {
		if err := c.closeSupersededUpdatePR(ctx, logger, pr, repo, pkgPaths, selected); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) closeSupersededUpdatePR(ctx context.Context, logger *slog.Logger, pr *github.PullRequest, repo string, pkgPaths map[string]string, selected map[string]struct{}) error {
	if pr.GetUser().GetLogin() != updatePRAuthor || pr.GetHead().GetRepo().GetFullName() != repo {
		return nil
	}
	superseded, err := c.supersededUpdates(logger, pr, pkgPaths, selected)
	if err != nil {
		return err
	}
	if len(superseded) == 0 {
		return nil
	}
	pkgName, ok, err := c.changedPackage(ctx, pr, pkgPaths, superseded)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	logger.Info("closing a superseded update pull request",
		"pull_request_number", pr.GetNumber(), "pkg_name", pkgName)
	if _, _, err := c.pull.Edit(ctx, c.param.RepoOwner, c.param.RepoName, pr.GetNumber(), &github.PullRequest{
		State: new("closed"),
	}); err != nil {
		return fmt.Errorf("close pull request #%d: %w", pr.GetNumber(), err)
	}
	return nil
}

func (c *Controller) supersededUpdates(logger *slog.Logger, pr *github.PullRequest, pkgPaths map[string]string, selected map[string]struct{}) (map[string]string, error) {
	superseded := make(map[string]string)
	for pkgName, proposedVersion := range proposedUpdates(pr.GetHead().GetRef(), pkgPaths) {
		if len(selected) != 0 {
			if _, ok := selected[pkgName]; !ok {
				continue
			}
		}
		body, err := afero.ReadFile(c.fs, pkgPaths[pkgName])
		if err != nil {
			return nil, fmt.Errorf("read pkg.yaml: %w", err)
		}
		currentVersion, err := c.getCurrentVersion(pkgName, string(body))
		if err != nil {
			return nil, fmt.Errorf("get the current version: %w", err)
		}
		closePR, err := isCurrentVersionSameOrNewer(currentVersion, proposedVersion)
		if err != nil {
			slogerr.WithError(logger, err).Warn("skip an incomparable update pull request",
				"pull_request_number", pr.GetNumber())
			continue
		}
		if closePR {
			superseded[pkgName] = proposedVersion
		}
	}
	return superseded, nil
}

func proposedUpdates(branch string, pkgPaths map[string]string) map[string]string {
	candidates := make(map[string]string)
	for pkgName := range pkgPaths {
		version, ok := strings.CutPrefix(branch, updateBranchPrefix+pkgName+"-")
		if ok {
			candidates[pkgName] = version
		}
	}
	return candidates
}

func (c *Controller) changedPackage(ctx context.Context, pr *github.PullRequest, pkgPaths, candidates map[string]string) (string, bool, error) {
	files, _, err := c.pull.ListFiles(ctx, c.param.RepoOwner, c.param.RepoName, pr.GetNumber(), nil)
	if err != nil {
		return "", false, fmt.Errorf("list files of pull request #%d: %w", pr.GetNumber(), err)
	}
	if len(files) != 1 {
		return "", false, nil
	}
	for pkgName := range candidates {
		if files[0].GetFilename() == pkgPaths[pkgName] {
			return pkgName, true, nil
		}
	}
	return "", false, nil
}

func isCurrentVersionSameOrNewer(currentVersion, proposedVersion string) (bool, error) {
	current, currentPrefix, err := versiongetter.GetVersionAndPrefix(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse the current version %s: %w", currentVersion, err)
	}
	if current == nil {
		return false, fmt.Errorf("the current version isn't valid: %s", currentVersion)
	}
	proposed, proposedPrefix, err := versiongetter.GetVersionAndPrefix(proposedVersion)
	if err != nil {
		return false, fmt.Errorf("parse the proposed version %s: %w", proposedVersion, err)
	}
	if proposed == nil {
		return false, fmt.Errorf("the proposed version isn't valid: %s", proposedVersion)
	}
	if currentPrefix != proposedPrefix {
		return false, nil
	}
	return currentVersion == proposedVersion || current.GreaterThan(proposed), nil
}

func packageNameFromPath(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "pkgs/"), "/pkg.yaml")
}
