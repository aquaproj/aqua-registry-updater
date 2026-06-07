package controller

import (
	"testing"
)

func Test_compareVersion(t *testing.T) {
	t.Parallel()
	data := []struct {
		name           string
		currentVersion string
		newVersion     string
		f              bool
		isErr          bool
	}{
		{
			name:           "normal",
			currentVersion: "v2.0.0",
			newVersion:     "v2.1.0",
			f:              true,
		},
		{
			name:           "old",
			currentVersion: "v2.1.0",
			newVersion:     "v2.0.0",
		},
		{
			name:           "different prefix",
			currentVersion: "edge-v2.0.0",
			newVersion:     "stable-v2.1.0",
		},
		{
			name:           "same prefix",
			currentVersion: "cli-v2.0.0",
			newVersion:     "cli-v2.1.0",
			f:              true,
		},
		{
			name:           "same prefix but old",
			currentVersion: "cli-v2.1.0",
			newVersion:     "cli-v2.0.0",
			f:              false,
		},
		{
			name:           "current version is a commit hash",
			currentVersion: "cd684900348e6c23335064bf74c8368e3abcec5e",
			newVersion:     "v0.1.3",
			isErr:          true,
		},
		{
			name:           "new version is a commit hash",
			currentVersion: "v0.1.3",
			newVersion:     "cd684900348e6c23335064bf74c8368e3abcec5e",
			isErr:          true,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			f, err := compareVersion(d.currentVersion, d.newVersion)
			if err != nil {
				if d.isErr {
					return
				}
				t.Fatal(err)
			}
			if d.isErr {
				t.Fatal("error must be returned")
			}
			if f != d.f {
				t.Fatalf("wanted %v, got %v", d.f, f)
			}
		})
	}
}
