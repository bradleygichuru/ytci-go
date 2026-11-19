package species

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseSource() SourceWrite {
	return SourceWrite{
		Publisher:    "GBIF",
		Citation:     "GBIF Backbone Taxonomy. doi:10.15468/39omei",
		AccessedAt:   "2026-09-01",
		LicenceCode:  "CC-BY-4.0",
		PermittedUse: "taxonomy_only",
		Attribution:  "GBIF.org (2026)",
	}
}

// TestSourceLicenceBoundary covers the four source rules that must never be
// relaxed: IUCN metadata only, KWS written permission, GBIF occurrence
// licences and Wikipedia link-out.
func TestSourceLicenceBoundary(t *testing.T) {
	t.Run("iucn prose is rejected", func(t *testing.T) {
		w := baseSource()
		w.Publisher = "IUCN"
		w.PermittedUse = "prose"
		err := validateSourceWrite(w)
		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "permittedUse", ve.Field)
	})

	t.Run("iucn category metadata is allowed", func(t *testing.T) {
		w := baseSource()
		w.Publisher = "iucn"
		w.PermittedUse = "taxonomy_only"
		require.NoError(t, validateSourceWrite(w))
	})

	t.Run("kws bulk copy needs a permission reference", func(t *testing.T) {
		w := baseSource()
		w.Publisher = "Kenya Wildlife Service"
		w.PermittedUse = "prose"
		err := validateSourceWrite(w)
		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "licenceCode", ve.Field)

		w.LicenceCode = "PERMISSION-KWS-2026-014"
		require.NoError(t, validateSourceWrite(w))
	})

	t.Run("kws link out is allowed", func(t *testing.T) {
		w := baseSource()
		w.Publisher = "KWS"
		w.PermittedUse = "link_only"
		require.NoError(t, validateSourceWrite(w))
	})

	t.Run("gbif occurrence must be cc0 or cc by", func(t *testing.T) {
		w := baseSource()
		w.PermittedUse = "occurrence"
		w.LicenceCode = "CC-BY-NC-4.0"
		require.Error(t, validateSourceWrite(w))

		w.LicenceCode = "CC0-1.0"
		require.NoError(t, validateSourceWrite(w))

		w.LicenceCode = "CC-BY-4.0"
		require.NoError(t, validateSourceWrite(w))
	})

	t.Run("wikipedia prose is rejected", func(t *testing.T) {
		w := baseSource()
		w.Publisher = "Wikipedia"
		w.PermittedUse = "prose"
		require.Error(t, validateSourceWrite(w))
	})

	t.Run("no source means no publish", func(t *testing.T) {
		require.Error(t, validateSourceWrite(SourceWrite{}))
	})
}
