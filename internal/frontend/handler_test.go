package frontend

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// mockFS builds an in-memory filesystem matching the key parts of a
// Next.js static export with dynamic routes.
func mockFS() fs.FS {
	return fstest.MapFS{
		"index.html":                                   &fstest.MapFile{Data: []byte("dashboard")},
		"analyses.html":                                &fstest.MapFile{Data: []byte("analyses list")},
		"projects.html":                                &fstest.MapFile{Data: []byte("projects list")},
		"projects/new.html":                            &fstest.MapFile{Data: []byte("new project")},
		"projects/_.html":                              &fstest.MapFile{Data: []byte("project detail")},
		"projects/_.txt":                               &fstest.MapFile{Data: []byte("project rsc")},
		"projects/_/analyses/_.html":                   &fstest.MapFile{Data: []byte("analysis detail")},
		"projects/_/analyses/_.txt":                    &fstest.MapFile{Data: []byte("analysis rsc")},
		"projects/_/analyses/_/__next._full.txt":       &fstest.MapFile{Data: []byte("rsc full")},
		"projects/_/analyses/_/__next._tree.txt":       &fstest.MapFile{Data: []byte("rsc tree")},
		"groups.html":                                  &fstest.MapFile{Data: []byte("groups list")},
		"groups/_.html":                                &fstest.MapFile{Data: []byte("group detail")},
		"groups/_.txt":                                 &fstest.MapFile{Data: []byte("group rsc")},
		"groups/new.html":                              &fstest.MapFile{Data: []byte("new group")},
		"login.html":                                   &fstest.MapFile{Data: []byte("login")},
		"findings.html":                                &fstest.MapFile{Data: []byte("findings")},
		"settings.html":                                &fstest.MapFile{Data: []byte("settings")},
		"admin/users.html":                             &fstest.MapFile{Data: []byte("admin users")},
		"admin/backups.html":                           &fstest.MapFile{Data: []byte("admin backups")},
	}
}

func TestResolveDynamicRoute(t *testing.T) {
	fsys := mockFS()

	tests := []struct {
		name     string
		urlPath  string
		expected string
	}{
		// Static pages — no dynamic resolution needed (handled before resolveDynamicRoute)
		// but resolveDynamicRoute should return "" for them since they exist as exact files.

		// Project detail: /projects/UUID → projects/_.html
		{
			name:     "project detail",
			urlPath:  "projects/some-uuid",
			expected: "projects/_.html",
		},
		// Project detail RSC: /projects/UUID.txt → projects/_.txt
		{
			name:     "project detail RSC",
			urlPath:  "projects/some-uuid.txt",
			expected: "projects/_.txt",
		},
		// Project new should prefer literal over wildcard
		{
			name:     "project new prefers literal",
			urlPath:  "projects/new",
			expected: "projects/new.html",
		},

		// Analysis detail: /projects/UUID/analyses/UUID2 → projects/_/analyses/_.html
		{
			name:     "analysis detail",
			urlPath:  "projects/some-uuid/analyses/another-uuid",
			expected: "projects/_/analyses/_.html",
		},
		// Analysis detail RSC: /projects/UUID/analyses/UUID2.txt → projects/_/analyses/_.txt
		{
			name:     "analysis detail RSC",
			urlPath:  "projects/some-uuid/analyses/another-uuid.txt",
			expected: "projects/_/analyses/_.txt",
		},
		// Analysis detail __next RSC payload
		{
			name:     "analysis detail __next RSC",
			urlPath:  "projects/some-uuid/analyses/another-uuid/__next._full.txt",
			expected: "projects/_/analyses/_/__next._full.txt",
		},
		{
			name:     "analysis detail __next tree RSC",
			urlPath:  "projects/some-uuid/analyses/another-uuid/__next._tree.txt",
			expected: "projects/_/analyses/_/__next._tree.txt",
		},

		// Group detail: /groups/UUID → groups/_.html
		{
			name:     "group detail",
			urlPath:  "groups/some-uuid",
			expected: "groups/_.html",
		},
		// Group detail RSC
		{
			name:     "group detail RSC",
			urlPath:  "groups/some-uuid.txt",
			expected: "groups/_.txt",
		},
		// Group new should prefer literal over wildcard
		{
			name:     "group new prefers literal",
			urlPath:  "groups/new",
			expected: "groups/new.html",
		},

		// Unknown deep path → empty (fallback to index.html in handler)
		{
			name:     "unknown path",
			urlPath:  "nonexistent/deep/path",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveDynamicRoute(fsys, tt.urlPath)
			if got != tt.expected {
				t.Errorf("resolveDynamicRoute(%q) = %q, want %q", tt.urlPath, got, tt.expected)
			}
		})
	}
}
