package dalec

import "testing"

type tableEntry struct {
	PostInstall
	shouldFailVaildation bool
	desc                 string
	expectedOutput       []ArtifactSymlinkConfig
	expectedNumLinks     int
}

func TestGetSymlinks(t *testing.T) {
	table := []tableEntry{
		{
			desc: "invalid SymlinkTarget should fail validation: empty target",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"oldpath": {},
				},
			},
			shouldFailVaildation: true,
		},
		{
			desc: "invalid SymlinkTarget should fail validation: empty key, valid target(paths)",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"": {
						Paths: []string{"/newpath_z", "/newpath_a"},
					},
				},
			},
			shouldFailVaildation: true,
		},
		{
			desc: "invalid SymlinkTarget should fail validation: empty key: valid target(path)",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"": {
						Path: "/newpath_z",
					},
				},
			},
			shouldFailVaildation: true,
		},
		{
			desc: "invalid SymlinkTarget should fail validation: all symlink 'newpaths' should be unique",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"perfectly_valid": {
						Path: "/also_valid",
					},
					"also_perfectly_valid": {
						Paths: []string{"/also_valid"},
					},
				},
			},
			shouldFailVaildation: true,
		},
		{
			desc: "invalid SymlinkTarget should fail validation: path and paths are mutually exclusive",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"perfectly_valid": {
						Path:  "/also_valid",
						Paths: []string{"/also_valid_too", "also_valid_too,_also"},
					},
				},
			},
			shouldFailVaildation: true,
		},
		{
			desc: "should be able to create multiple symlinks to the same target, with the correct ordering",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"oldpath": {
						Paths: []string{"/newpath_z", "/newpath_a"},
					},
				},
			},
			expectedOutput: []ArtifactSymlinkConfig{
				{
					Source: "oldpath",
					Dest:   "/newpath_a",
				},
				{
					Source: "oldpath",
					Dest:   "/newpath_z",
				},
			},
			expectedNumLinks: 2,
		},
		{
			desc: "combine Path and Paths with correct ordering",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"oldpath2": {
						Paths: []string{"/newpath_z", "/newpath_a"},
					},
					"oldpath1": {
						Path: "/newpath1",
					},
				},
			},
			expectedOutput: []ArtifactSymlinkConfig{
				{
					Source: "oldpath1",
					Dest:   "/newpath1",
				},
				{
					Source: "oldpath2",
					Dest:   "/newpath_a",
				},
				{
					Source: "oldpath2",
					Dest:   "/newpath_z",
				},
			},
			expectedNumLinks: 3,
		},
		{
			desc: "just Path",
			PostInstall: PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"oldpath3": {
						Path: "/newpath3",
					},
					"oldpath2": {
						Path: "/newpath2",
					},
					"oldpath1": {
						Path: "/newpath1",
					},
				},
			},
			expectedOutput: []ArtifactSymlinkConfig{
				{
					Source: "oldpath1",
					Dest:   "/newpath1",
				},
				{
					Source: "oldpath2",
					Dest:   "/newpath2",
				},
				{
					Source: "oldpath3",
					Dest:   "/newpath3",
				},
			},
			expectedNumLinks: 3,
		},
	}

	for _, test := range table {
		if passed := t.Run(test.desc, func(t *testing.T) {
			p := test.PostInstall

			if err := p.validate(); err != nil {
				if !test.shouldFailVaildation {
					t.Logf("input failed validation: %s", err)
					t.Fail()
				}

				return
			}

			if test.shouldFailVaildation { // err was nil, but shouldn't have been
				t.Logf("input should have failed validation, but succeeded:\n%#v", test.PostInstall)
				t.Fail()
				return
			}

			actualSymlinks := p.GetSymlinks()
			if len(actualSymlinks) != test.expectedNumLinks {
				t.Logf("expected %d links, but there were %d", test.expectedNumLinks, len(actualSymlinks))
				t.Fail()
			}

			for i := range actualSymlinks {
				actual := actualSymlinks[i]
				expected := test.expectedOutput[i]

				if actual != expected {
					t.Logf("expected:\n%#v\n================found:\n%#v\n", expected, actual)
					t.Fail()
				}
			}
		}); !passed {
			t.Fail()
		}
	}
}
