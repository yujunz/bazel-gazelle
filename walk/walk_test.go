/* Copyright 2018 The Bazel Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package walk

import (
	"flag"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"github.com/bazelbuild/bazel-gazelle/testtools"
	"github.com/google/go-cmp/cmp"
)

func TestConfigureCallbackOrder(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{{Path: "a/b/"}})
	defer cleanup()

	var configureRels, callbackRels []string
	c, cexts := testConfig(t, dir)
	cexts = append(cexts, &testConfigurer{func(_ *config.Config, rel string, _ *rule.File) {
		configureRels = append(configureRels, rel)
	}})
	Walk(c, cexts, []string{dir}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		callbackRels = append(callbackRels, rel)
	})
	configureWant := []string{"", "a", "a/b"}
	if diff := cmp.Diff(configureWant, configureRels); diff != "" {
		t.Errorf("configure order (-want +got):\n%s", diff)
	}
	callbackWant := []string{"a/b", "a", ""}
	if diff := cmp.Diff(callbackWant, callbackRels); diff != "" {
		t.Errorf("callback order (-want +got):\n%s", diff)
	}
}

func TestUpdateDirs(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{
		{Path: "update/sub/"},
		{Path: "update/sub/sub/"},
		{
			Path:    "update/ignore/BUILD.bazel",
			Content: "# gazelle:ignore",
		},
		{Path: "update/ignore/sub/"},
		{
			Path:    "update/error/BUILD.bazel",
			Content: "(",
		},
		{Path: "update/error/sub/"},
	})
	defer cleanup()

	type visitSpec struct {
		Rel    string
		Update bool
	}
	for _, tc := range []struct {
		desc string
		rels []string
		mode Mode
		want []visitSpec
	}{
		{
			desc: "visit_all_update_subdirs",
			rels: []string{"update"},
			mode: VisitAllUpdateSubdirsMode,
			want: []visitSpec{
				{"update/error/sub", true},
				{"update/error", false},
				{"update/ignore/sub", true},
				{"update/ignore", false},
				{"update/sub/sub", true},
				{"update/sub", true},
				{"update", true},
				{"", false},
			},
		}, {
			desc: "visit_all_update_dirs",
			rels: []string{"update", "update/ignore/sub"},
			mode: VisitAllUpdateDirsMode,
			want: []visitSpec{
				{"update/error/sub", false},
				{"update/error", false},
				{"update/ignore/sub", true},
				{"update/ignore", false},
				{"update/sub/sub", false},
				{"update/sub", false},
				{"update", true},
				{"", false},
			},
		}, {
			desc: "update_dirs",
			rels: []string{"update", "update/ignore/sub"},
			mode: UpdateDirsMode,
			want: []visitSpec{
				{"update/ignore/sub", true},
				{"update", true},
			},
		}, {
			desc: "update_subdirs",
			rels: []string{"update/ignore", "update/sub"},
			mode: UpdateSubdirsMode,
			want: []visitSpec{
				{"update/ignore/sub", true},
				{"update/ignore", false},
				{"update/sub/sub", true},
				{"update/sub", true},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			c, cexts := testConfig(t, dir)
			dirs := make([]string, len(tc.rels))
			for i, rel := range tc.rels {
				dirs[i] = filepath.Join(dir, filepath.FromSlash(rel))
			}
			var visits []visitSpec
			Walk(c, cexts, dirs, tc.mode, func(_ string, rel string, _ *config.Config, update bool, _ *rule.File, _, _, _ []string) {
				visits = append(visits, visitSpec{rel, update})
			})
			if diff := cmp.Diff(tc.want, visits); diff != "" {
				t.Errorf("Walk visits (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCustomBuildName(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{
		{
			Path:    "BUILD.bazel",
			Content: "# gazelle:build_file_name BUILD.test",
		}, {
			Path: "BUILD",
		}, {
			Path: "sub/BUILD.test",
		}, {
			Path: "sub/BUILD.bazel",
		},
	})
	defer cleanup()

	c, cexts := testConfig(t, dir)
	var rels []string
	Walk(c, cexts, []string{dir}, VisitAllUpdateSubdirsMode, func(_ string, _ string, _ *config.Config, _ bool, f *rule.File, _, _, _ []string) {
		rel, err := filepath.Rel(c.RepoRoot, f.Path)
		if err != nil {
			t.Error(err)
		} else {
			rels = append(rels, filepath.ToSlash(rel))
		}
	})
	want := []string{
		"sub/BUILD.test",
		"BUILD.bazel",
	}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestExcludeFiles(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{
		{
			Path: "BUILD.bazel",
			Content: `
# gazelle:exclude **/*.pb.go
# gazelle:exclude *.gen.go
# gazelle:exclude a.go
# gazelle:exclude c/**/b
# gazelle:exclude gen
# gazelle:exclude ign
# gazelle:exclude sub/b.go

gen(
    name = "x",
    out = "gen",
)
`,
		},
		{Path: ".dot"},       // not ignored
		{Path: "_blank"},     // not ignored
		{Path: "a/a.proto"},  // not ignored
		{Path: "a/b.gen.go"}, // not ignored

		{Path: "a.gen.go"},        // ignored by '*.gen.go'
		{Path: "a.go"},            // ignored by 'a.go'
		{Path: "a.pb.go"},         // ignored by '**/*.pb.go'
		{Path: "a/a.pb.go"},       // ignored by '**/*.pb.go'
		{Path: "a/b/a.pb.go"},     // ignored by '**/*.pb.go'
		{Path: "c/x/b/foo"},       // ignored by 'c/**/b'
		{Path: "c/x/y/b/bar"},     // ignored by 'c/**/b'
		{Path: "c/x/y/b/foo/bar"}, // ignored by 'c/**/b'
		{Path: "ign/bad"},         // ignored by 'ign'
		{Path: "sub/b.go"},        // ignored by 'sub/b.go'
	})
	defer cleanup()

	c, cexts := testConfig(t, dir)
	var files []string
	Walk(c, cexts, []string{dir}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, regularFiles, genFiles []string) {
		for _, f := range regularFiles {
			files = append(files, path.Join(rel, f))
		}
		for _, f := range genFiles {
			files = append(files, path.Join(rel, f))
		}
	})
	want := []string{"a/a.proto", "a/b.gen.go", ".dot", "BUILD.bazel", "_blank"}
	if diff := cmp.Diff(want, files); diff != "" {
		t.Errorf("Walk files (-want +got):\n%s", diff)
	}
}

func TestExcludeSelf(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{
		{
			Path: "BUILD.bazel",
		}, {
			Path:    "sub/BUILD.bazel",
			Content: "# gazelle:exclude .",
		}, {
			Path: "sub/below/BUILD.bazel",
		},
	})
	defer cleanup()

	c, cexts := testConfig(t, dir)
	var rels []string
	Walk(c, cexts, []string{dir}, VisitAllUpdateDirsMode, func(_ string, rel string, _ *config.Config, _ bool, f *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})

	want := []string{""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestGeneratedFiles(t *testing.T) {
	dir, cleanup := testtools.CreateFiles(t, []testtools.FileSpec{
		{
			Path: "BUILD.bazel",
			Content: `
unknown_rule(
    name = "blah1",
    out = "gen1",
)

unknown_rule(
    name = "blah2",
    outs = [
        "gen2",
        "gen-and-static",
    ],
)
`,
		},
		{Path: "gen-and-static"},
		{Path: "static"},
	})
	defer cleanup()

	c, cexts := testConfig(t, dir)
	var regularFiles, genFiles []string
	Walk(c, cexts, []string{dir}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, reg, gen []string) {
		for _, f := range reg {
			regularFiles = append(regularFiles, path.Join(rel, f))
		}
		for _, f := range gen {
			genFiles = append(genFiles, path.Join(rel, f))
		}
	})
	regWant := []string{"BUILD.bazel", "gen-and-static", "static"}
	if diff := cmp.Diff(regWant, regularFiles); diff != "" {
		t.Errorf("Walk regularFiles (-want +got):\n%s", diff)
	}
	genWant := []string{"gen1", "gen2", "gen-and-static"}
	if diff := cmp.Diff(genWant, genFiles); diff != "" {
		t.Errorf("Walk genFiles (-want +got):\n%s", diff)
	}
}

func TestSymlinksBasic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{Path: "root/a.go", Content: "package a"},
		{Path: "root/b", Symlink: "../b"},   // symlink outside repo is followed
		{Path: "root/c", Symlink: "c"},      // symlink inside repo is not followed.
		{Path: "root/d", Symlink: "../b/d"}, // symlink under root/b not followed
		{Path: "root/e", Symlink: "../e"},
		{Path: "c/c.go", Symlink: "package c"},
		{Path: "b/b.go", Content: "package b"},
		{Path: "b/d/d.go", Content: "package d"},
		{Path: "e/loop", Symlink: "loop2"}, // symlink loop
		{Path: "e/loop2", Symlink: "loop"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	root := filepath.Join(dir, "root")
	c, cexts := testConfig(t, root)
	var rels []string
	Walk(c, cexts, []string{root}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{"b/d", "b", "e", ""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestSymlinksIgnore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{
			Path:    "root/BUILD",
			Content: "# gazelle:exclude b",
		},
		{Path: "root/b", Symlink: "../b"},
		{Path: "b/b.go", Content: "package b"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	root := filepath.Join(dir, "root")
	c, cexts := testConfig(t, root)
	var rels []string
	Walk(c, cexts, []string{root}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestSymlinksMixIgnoredAndNonIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{
			Path:    "root/BUILD",
			Content: "# gazelle:exclude b",
		},
		{Path: "root/b", Symlink: "../b"},  // ignored
		{Path: "root/b2", Symlink: "../b"}, // not ignored
		{Path: "b/b.go", Content: "package b"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	root := filepath.Join(dir, "root")
	c, cexts := testConfig(t, root)
	var rels []string
	Walk(c, cexts, []string{root}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{"b2", ""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestSymlinksChained(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{Path: "root/b", Symlink: "../link0"},
		{Path: "link0", Symlink: "b"},
		{Path: "root/b2", Symlink: "../b"},
		{Path: "b/b.go", Content: "package b"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	root := filepath.Join(dir, "root")
	c, cexts := testConfig(t, root)
	var rels []string
	Walk(c, cexts, []string{root}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{"b", ""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestSymlinksDangling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{Path: "root/b", Symlink: "../b"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	root := filepath.Join(dir, "root")
	c, cexts := testConfig(t, root)
	var rels []string
	Walk(c, cexts, []string{root}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{""}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func TestSymlinksFollow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks not supported on windows")
	}
	files := []testtools.FileSpec{
		{Path: "staging/src/k8s.io/api/"},
		{Path: "staging/src/k8s.io/BUILD.bazel", Content: "# gazelle:exclude api"},
		{Path: "vendor/k8s.io/api", Symlink: "../../staging/src/k8s.io/api"},
		{Path: "vendor/BUILD.bazel", Content: "# gazelle:follow k8s.io/api"},
	}
	dir, cleanup := testtools.CreateFiles(t, files)
	defer cleanup()

	c, cexts := testConfig(t, dir)
	var rels []string
	Walk(c, cexts, []string{dir}, VisitAllUpdateSubdirsMode, func(_ string, rel string, _ *config.Config, _ bool, _ *rule.File, _, _, _ []string) {
		rels = append(rels, rel)
	})
	want := []string{
		"staging/src/k8s.io",
		"staging/src",
		"staging",
		"vendor/k8s.io/api",
		"vendor/k8s.io",
		"vendor",
		"",
	}
	if diff := cmp.Diff(want, rels); diff != "" {
		t.Errorf("Walk relative paths (-want +got):\n%s", diff)
	}
}

func testConfig(t *testing.T, dir string) (*config.Config, []config.Configurer) {
	args := []string{"-repo_root", dir}
	cexts := []config.Configurer{&config.CommonConfigurer{}, &Configurer{}}
	c := testtools.NewTestConfig(t, cexts, nil, args)
	return c, cexts
}

type testConfigurer struct {
	configure func(c *config.Config, rel string, f *rule.File)
}

func (*testConfigurer) RegisterFlags(_ *flag.FlagSet, _ string, _ *config.Config) {}

func (*testConfigurer) CheckFlags(_ *flag.FlagSet, _ *config.Config) error { return nil }

func (*testConfigurer) KnownDirectives() []string { return nil }

func (tc *testConfigurer) Configure(c *config.Config, rel string, f *rule.File) {
	tc.configure(c, rel, f)
}
