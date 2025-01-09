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
				if test.shouldFailVaildation {
					return
				}

				t.Log("input failed validation")
				t.Fail()
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
