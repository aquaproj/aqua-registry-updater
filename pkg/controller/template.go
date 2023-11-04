package controller

import (
	"bytes"
	"text/template"
)

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
