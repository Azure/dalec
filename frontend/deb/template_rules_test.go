package deb

import (
	"testing"

	"github.com/Azure/dalec"
	"gotest.tools/v3/assert"
)

func TestRules_OverrideSystemd(t *testing.T) {
	newWrapper := func(units map[string]dalec.SystemdUnitConfig) *rulesWrapper {
		return &rulesWrapper{
			Spec: &dalec.Spec{
				Artifacts: dalec.Artifacts{
					Systemd: &dalec.SystemdConfiguration{
						Units: units,
					},
				},
			},
		}
	}

	t.Run("no units", func(t *testing.T) {
		w := newWrapper(nil)
		out, err := w.OverrideSystemd()
		assert.NilError(t, err)
		expect := ""
		assert.Equal(t, out.String(), expect)
	})

	t.Run("single unit", func(t *testing.T) {
		t.Run("enabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=foo
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("disabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=foo --no-enable
`
			assert.Equal(t, out.String(), expect)
		})
	})

	t.Run("multiple units", func(t *testing.T) {
		t.Run("enabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: true},
				"foo.socket":  {Enable: true},
				"bar.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar
	dh_installsystemd --name=foo
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("disabled", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
				"foo.socket":  {Enable: false},
				"bar.service": {Enable: false},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar --no-enable
	dh_installsystemd --name=foo --no-enable
`
			assert.Equal(t, out.String(), expect)
		})

		t.Run("mixed", func(t *testing.T) {
			w := newWrapper(map[string]dalec.SystemdUnitConfig{
				"foo.service": {Enable: false},
				"foo.socket":  {Enable: true},
				"bar.service": {Enable: true},
			})

			out, err := w.OverrideSystemd()
			assert.NilError(t, err)
			expect := `override_dh_installsystemd:
	dh_installsystemd --name=bar
	dh_installsystemd --name=foo --no-enable
	[ -f debian/postinst ] || (echo '#!/bin/sh' > debian/postinst; echo 'set -e' >> debian/postinst)
	[ -x debian/postinst ] || chmod +x debian/postinst
	cat debian/dalec/custom_systemd_postinst.sh.partial >> debian/postinst
`
			assert.Equal(t, out.String(), expect)
		})
	})
}
