package translation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlaceholders(t *testing.T) {
	placeholders := Placeholders(
		"Hello %name%, {{ limit }}, @username, {title}, " +
			"{count, plural, one {item} other {items}}",
	)
	assert.Contains(t, placeholders, "%name%")
	assert.Contains(t, placeholders, "{{ limit }}")
	assert.Contains(t, placeholders, "@username")
	assert.Contains(t, placeholders, "{title}")
	assert.Contains(t, placeholders, "title")
	assert.Contains(t, placeholders, "count")
}
