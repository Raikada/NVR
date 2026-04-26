package httpp

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/stretchr/testify/require"
)

func TestCredentials(t *testing.T) {
	t.Run("user and pass in basic", func(t *testing.T) {
		h := &http.Request{
			URL: &url.URL{},
			Header: http.Header{
				"Authorization": []string{
					"Basic bXl1c2VyOm15cGFzcw==",
				},
			},
		}

		c := Credentials(h)

		require.Equal(t, &auth.Credentials{
			User: "myuser",
			Pass: "mypass",
		}, c)
	})

	t.Run("colon-separated bearer is now opaque (D6)", func(t *testing.T) {
		// The non-standard "Bearer user:pass" form was removed per D6.
		// A token that happens to contain a colon is now treated
		// opaquely — clients that need username+password must use
		// HTTP Basic.
		h := &http.Request{
			URL: &url.URL{},
			Header: http.Header{
				"Authorization": []string{
					"Bearer myuser:mypass",
				},
			},
		}

		c := Credentials(h)

		require.Equal(t, &auth.Credentials{
			Token: "myuser:mypass",
		}, c)
	})

	t.Run("token in bearer", func(t *testing.T) {
		h := &http.Request{
			URL: &url.URL{},
			Header: http.Header{
				"Authorization": []string{
					"Bearer testing123",
				},
			},
		}

		c := Credentials(h)

		require.Equal(t, &auth.Credentials{
			Token: "testing123",
		}, c)
	})

	t.Run("user and pass and token", func(t *testing.T) {
		h := &http.Request{
			URL: &url.URL{},
			Header: http.Header{
				"Authorization": []string{
					"Basic bXl1c2VyOm15cGFzcw==",
					"Bearer testing123",
				},
			},
		}

		c := Credentials(h)

		require.Equal(t, &auth.Credentials{
			Token: "testing123",
		}, c)
	})
}
