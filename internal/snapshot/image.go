package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"lamina/internal/catalog"
	"lamina/internal/versions"
	"os"
)

const MaxImage = 128 << 20

// Image payload formats. FormatLegacy (1) predates row checks: its catalog
// cannot declare checks, and it is restored explicitly with zero declared
// checks rather than being reinterpreted under any current declarations.
// FormatCurrent (2) catalogs may declare checks; every retained version
// still readable within a check's effective scope is validated on decode.
const (
	FormatLegacy  = 1
	FormatCurrent = 2
)

type Image struct {
	Format   int              `json:"format"`
	Sequence uint64           `json:"sequence"`
	Horizon  uint64           `json:"horizon"`
	Catalog  *catalog.Catalog `json:"catalog"`
	Store    *versions.Store  `json:"store"`
}
type Envelope struct {
	Format  int             `json:"format"`
	Digest  string          `json:"sha256"`
	Payload json.RawMessage `json:"payload"`
}

func Encode(image Image) ([]byte, error) {
	if image.Format != FormatLegacy && image.Format != FormatCurrent {
		return nil, fmt.Errorf("image format must be %d or %d", FormatLegacy, FormatCurrent)
	}
	b, err := json.Marshal(image)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	return json.Marshal(Envelope{Format: 1, Digest: hex.EncodeToString(h[:]), Payload: b})
}
func Decode(b []byte) (Image, error) {
	var image Image
	var env Envelope
	if len(b) > MaxImage {
		return image, fmt.Errorf("image exceeds limit")
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return image, err
	}
	h := sha256.Sum256(env.Payload)
	if env.Format != 1 || hex.EncodeToString(h[:]) != env.Digest {
		return image, fmt.Errorf("image format or digest mismatch")
	}
	if err := json.Unmarshal(env.Payload, &image); err != nil {
		return image, err
	}
	if err := Validate(image); err != nil {
		return image, err
	}
	if image.Catalog.Checks == nil {
		// Legacy images carry no declarations; normalize to an explicit
		// empty set so replay can append new declarations.
		image.Catalog.Checks = map[string]catalog.Check{}
	}
	return image, nil
}
func Read(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxImage+1))
	if err != nil {
		return Image{}, err
	}
	return Decode(b)
}
func Validate(image Image) error {
	if image.Format != FormatLegacy && image.Format != FormatCurrent {
		return fmt.Errorf("invalid image header")
	}
	if image.Horizon > image.Sequence || image.Catalog == nil || image.Store == nil || image.Catalog.Relations == nil || image.Catalog.Indexes == nil || image.Store.Tables == nil {
		return fmt.Errorf("invalid image header")
	}
	if image.Format == FormatLegacy && len(image.Catalog.Checks) > 0 {
		return fmt.Errorf("format %d image cannot declare checks", FormatLegacy)
	}
	if image.Format == FormatCurrent && image.Catalog.Checks == nil {
		return fmt.Errorf("invalid image header")
	}
	for name, r := range image.Catalog.Relations {
		if name != r.Name || r.Created == 0 || r.Created > image.Sequence {
			return fmt.Errorf("invalid catalog sequence")
		}
		if err := r.Validate(); err != nil {
			return err
		}
	}
	for name, definition := range image.Catalog.Indexes {
		relation, ok := image.Catalog.Relations[definition.Relation]
		if !ok || name != definition.Name || !catalog.ValidName(name) || definition.Created < relation.Created || definition.Created > image.Sequence {
			return fmt.Errorf("invalid index metadata")
		}
		if _, ok := relation.Column(definition.Column); !ok {
			return fmt.Errorf("invalid index column")
		}
	}
	for name, definition := range image.Catalog.Checks {
		relation, ok := image.Catalog.Relations[definition.Relation]
		if !ok || name != definition.Name || !catalog.ValidName(name) || definition.Created < relation.Created || definition.Created > image.Sequence {
			return fmt.Errorf("invalid check metadata")
		}
		if err := definition.Validate(relation); err != nil {
			return fmt.Errorf("invalid check %s: %w", name, err)
		}
	}
	for name, rows := range image.Store.Tables {
		r, ok := image.Catalog.Relations[name]
		if !ok {
			return fmt.Errorf("image references unknown relation")
		}
		for key, chain := range rows {
			var last uint64
			if len(chain) == 0 {
				return fmt.Errorf("empty version chain")
			}
			for _, v := range chain {
				if v.Sequence <= last || v.Sequence > image.Sequence || v.Sequence <= r.Created {
					return fmt.Errorf("invalid version order")
				}
				last = v.Sequence
				if !v.Deleted {
					row, err := r.Normalize(v.Row)
					if err != nil || r.Key(row) != key {
						return fmt.Errorf("invalid stored tuple")
					}
				}
			}
		}
	}
	for _, definition := range image.Catalog.Indexes {
		if !definition.Unique {
			continue
		}
		seen := map[string]bool{}
		for _, chain := range image.Store.Tables[definition.Relation] {
			version, exists := versions.Visible(chain, image.Sequence)
			if !exists {
				continue
			}
			value := version.Row[definition.Column]
			if value.Null() {
				continue
			}
			if seen[value.Key()] {
				return fmt.Errorf("image violates unique index %s", definition.Name)
			}
			seen[value.Key()] = true
		}
	}
	if err := ValidateChecks(image.Catalog, image.Store, image.Horizon); err != nil {
		return err
	}
	return nil
}

// ValidateChecks verifies that the durable state satisfies every declared
// check inside its effective scope. A check created at sequence C governs
// snapshots at C-1 and later, so the validated set is exactly the retained
// versions still readable at max(horizon, C-1) or beyond — not every
// historical version, and not only the latest one. It is used both for
// image validation and for post-replay recovery checks.
func ValidateChecks(c *catalog.Catalog, s *versions.Store, horizon uint64) error {
	for _, definition := range c.CheckList() {
		chains := s.Tables[definition.Relation]
		if len(chains) == 0 {
			continue
		}
		start := horizon
		if definition.Created > 0 && definition.Created-1 > start {
			start = definition.Created - 1
		}
		for key, chain := range chains {
			for _, v := range versions.ReadableFrom(chain, start) {
				if v.Deleted {
					continue
				}
				if definition.Violates(v.Row) {
					return fmt.Errorf("check %s violated by retained row of %s key %s at sequence %d", definition.Name, definition.Relation, key, v.Sequence)
				}
			}
		}
	}
	return nil
}
