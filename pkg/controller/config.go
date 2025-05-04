package controller

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

func (c *Controller) readConfig(path string, cfg *Config) error {
	f, err := c.fs.Open(path)
	if err != nil {
		return fmt.Errorf("open a configuration file: %w", err)
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("read a configuration file as YAML: %w", err)
	}
	return nil
}

type Config struct {
	Limit             int
	ContainerRegistry *ContainerRegistry `yaml:"container_registry"`
	IgnorePackages    []string           `yaml:"ignore_packages"`
	Templates         *Templates
	compiledTemplates *CompiledTemplates
	Scaffold          bool
}

func (c *Config) SetDefault(repo string) error { //nolint:cyclop,funlen
	if c.Limit == 0 {
		c.Limit = 50
	}
	if c.ContainerRegistry == nil {
		return errors.New("container_registry is required")
	}
	if c.ContainerRegistry.Auth == nil {
		return errors.New("container_registry.auth is required")
	}
	if c.ContainerRegistry.Registry == "" {
		c.ContainerRegistry.Registry = "ghcr.io"
	}
	if c.ContainerRegistry.Repository == "" {
		c.ContainerRegistry.Repository = repo
	}
	if c.ContainerRegistry.Auth.Username == "" {
		return errors.New("container_registry.auth.username is required")
	}
	if c.Templates == nil {
		c.Templates = &Templates{}
	}
	if c.Templates.PRTitle == "" {
		c.Templates.PRTitle = "chore: update {{.PackageName}} {{.CurrentVersion}} to {{.NewVersion}}"
	}
	if c.Templates.PRBody == "" {
		c.Templates.PRBody = `[{{.NewVersion}}]({{.ReleaseURL}}) [compare]({{.CompareURL}})

This pull request was created by [aqua-registry-updater](https://github.com/aquaproj/aqua-registry-updater).`
	}

	if c.Templates.TransferPRTitle == "" {
		c.Templates.TransferPRTitle = "fix({{.PackageName}}): transfer the repository to {{.NewRepoOwner}}/{{.NewRepoName}}"
	}
	if c.Templates.TransferPRBody == "" {
		c.Templates.TransferPRBody = `The GitHub Repository of the package "{{.PackageName}}" was transferred from [{{.RepoOwner}}/{{.RepoName}}](https://github.com/{{.RepoOwner}}/{{.RepoName}}) to [{{.NewRepoOwner}}/{{.NewRepoName}}](https://github.com/{{.NewRepoOwner}}/{{.NewRepoName}})

This pull request was created by [aqua-registry-updater](https://github.com/aquaproj/aqua-registry-updater).`
	}

	if c.Templates.ScaffoldPRTitle == "" {
		c.Templates.ScaffoldPRTitle = "Re-scaffold {{.PackageName}}"
	}
	if c.Templates.ScaffoldPRBody == "" {
		c.Templates.ScaffoldPRBody = `[registry](https://github.com/aquaproj/aqua-registry/tree/main/pkgs/{{.PackageName}}) | [repository](https://github.com/{{.RepoOwner}}/{{.RepoName}})

The command "cmdx s {{.PackageName}}" was run.

This pull request was created by [aqua-registry-updater](https://github.com/aquaproj/aqua-registry-updater).`
	}

	c.compiledTemplates = &CompiledTemplates{}

	prTitle, err := compileTemplate(c.Templates.PRTitle)
	if err != nil {
		return fmt.Errorf("compile a template pr_title: %w", err)
	}
	c.compiledTemplates.PRTitle = prTitle

	prBody, err := compileTemplate(c.Templates.PRBody)
	if err != nil {
		return fmt.Errorf("compile a template pr_body: %w", err)
	}
	c.compiledTemplates.PRBody = prBody

	transferPRTitle, err := compileTemplate(c.Templates.TransferPRTitle)
	if err != nil {
		return fmt.Errorf("compile a template transfer_pr_title: %w", err)
	}
	c.compiledTemplates.TransferPRTitle = transferPRTitle

	transferPRBody, err := compileTemplate(c.Templates.TransferPRBody)
	if err != nil {
		return fmt.Errorf("compile a template transfer_pr_body: %w", err)
	}
	c.compiledTemplates.TransferPRBody = transferPRBody

	scaffoldPRTitle, err := compileTemplate(c.Templates.ScaffoldPRTitle)
	if err != nil {
		return fmt.Errorf("compile a template scaffold_pr_title: %w", err)
	}
	c.compiledTemplates.ScaffoldPRTitle = scaffoldPRTitle

	scaffoldPRBody, err := compileTemplate(c.Templates.ScaffoldPRBody)
	if err != nil {
		return fmt.Errorf("compile a template scaffold_pr_body: %w", err)
	}
	c.compiledTemplates.ScaffoldPRBody = scaffoldPRBody

	return nil
}
