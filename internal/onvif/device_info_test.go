package onvif

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const getDeviceInformationResponseFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
  <env:Body>
    <tds:GetDeviceInformationResponse>
      <tds:Manufacturer>Hikvision</tds:Manufacturer>
      <tds:Model>DS-2CD2143G2-IS</tds:Model>
      <tds:FirmwareVersion>V5.7.2 build 230914</tds:FirmwareVersion>
      <tds:SerialNumber>HIK0000123456</tds:SerialNumber>
      <tds:HardwareId>884B</tds:HardwareId>
    </tds:GetDeviceInformationResponse>
  </env:Body>
</env:Envelope>`

const soapFaultFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope">
  <env:Body>
    <env:Fault>
      <env:Code><env:Value>env:Sender</env:Value></env:Code>
      <env:Reason><env:Text>Authentication required</env:Text></env:Reason>
    </env:Fault>
  </env:Body>
</env:Envelope>`

func TestGetDeviceInformation_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.Header.Get("Content-Type"), "soap+xml")
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "GetDeviceInformation")
		w.Header().Set("Content-Type", `application/soap+xml; charset=utf-8`)
		_, _ = io.WriteString(w, getDeviceInformationResponseFixture)
	}))
	defer srv.Close()

	c := &DeviceClient{XAddr: srv.URL, HTTPClient: srv.Client()}
	info, err := c.GetDeviceInformation(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Hikvision", info.Manufacturer)
	require.Equal(t, "DS-2CD2143G2-IS", info.Model)
	require.Equal(t, "V5.7.2 build 230914", info.FirmwareVersion)
	require.Equal(t, "HIK0000123456", info.SerialNumber)
	require.Equal(t, "884B", info.HardwareID)
}

func TestGetDeviceInformation_SoapFault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `application/soap+xml; charset=utf-8`)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, soapFaultFixture)
	}))
	defer srv.Close()

	c := &DeviceClient{XAddr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.GetDeviceInformation(context.Background())
	require.Error(t, err)
	var f *SOAPFault
	require.ErrorAs(t, err, &f)
	require.Contains(t, f.Reason, "Authentication required")
}

func TestGetDeviceInformation_HTTP401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "")
	}))
	defer srv.Close()

	c := &DeviceClient{XAddr: srv.URL, HTTPClient: srv.Client()}
	_, err := c.GetDeviceInformation(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication required")
}

func TestUsernameTokenHeader_PresentWhenCredsSupplied(t *testing.T) {
	got := usernameTokenHeader("admin", "secret", "bm9uY2U=", "2025-01-01T00:00:00.000Z")
	require.Contains(t, got, "<wsse:Security")
	require.Contains(t, got, "admin")
	require.Contains(t, got, "<wsse:Password")
	require.Contains(t, got, NSPasswordTyp)
	require.Contains(t, got, "<wsse:Nonce")
	require.NotContains(t, got, "secret") // password is digested, never sent plaintext
}

func TestUsernameTokenHeader_EmptyForAnonymous(t *testing.T) {
	require.Equal(t, "", usernameTokenHeader("", "secret", "n", "2025-01-01T00:00:00Z"))
}

func TestGetDeviceInformation_PassesWSSecurityWhenCredsProvided(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		w.Header().Set("Content-Type", `application/soap+xml; charset=utf-8`)
		_, _ = io.WriteString(w, getDeviceInformationResponseFixture)
	}))
	defer srv.Close()
	c := &DeviceClient{XAddr: srv.URL, Username: "admin", Password: "pw", HTTPClient: srv.Client()}
	_, err := c.GetDeviceInformation(context.Background())
	require.NoError(t, err)
	require.True(t, strings.Contains(captured, "<wsse:Security"))
	require.True(t, strings.Contains(captured, "admin"))
}

func TestParseSOAPFault(t *testing.T) {
	f := parseSOAPFault([]byte(soapFaultFixture))
	require.NotNil(t, f)
	require.Contains(t, f.Reason, "Authentication required")
	require.Contains(t, f.Code, "Sender")

	// Plain non-fault body returns nil.
	require.Nil(t, parseSOAPFault([]byte(getDeviceInformationResponseFixture)))
}
