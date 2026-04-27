package auth

import (
	"encoding/json"
	"fmt"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/golang-jwt/jwt/v5"
)

type jwtClaims struct {
	jwt.RegisteredClaims
	permissionsKey string
	permissions    []conf.AuthInternalUserPermission

	// ADR 0011 D2 user-level claims. Optional during the additive
	// rollout — issuers that haven't been updated to ADR 0011 yet still
	// produce tokens carrying only RegisteredClaims + the permissions
	// claim, and the recorder accepts those (with an unauthenticated /
	// service-account principal in the API layer).
	tenantID          string
	principalKind     string
	scope             []string
	scopeKind         string
	scopeTargetID     string
	clientFingerprint string
}

func (c *jwtClaims) UnmarshalJSON(b []byte) error {
	err := json.Unmarshal(b, &c.RegisteredClaims)
	if err != nil {
		return err
	}

	var claimMap map[string]json.RawMessage
	err = json.Unmarshal(b, &claimMap)
	if err != nil {
		return err
	}

	rawPermissions, ok := claimMap[c.permissionsKey]
	if !ok {
		return fmt.Errorf("claim '%s' not found inside JWT", c.permissionsKey)
	}

	err = jsonwrapper.Unmarshal(rawPermissions, &c.permissions)
	if err != nil {
		var str string
		err = json.Unmarshal(rawPermissions, &str)
		if err != nil {
			return err
		}

		err = jsonwrapper.Unmarshal([]byte(str), &c.permissions)
		if err != nil {
			return err
		}
	}

	// ADR 0011 D2 claims. Each is optional; absence is not an error.
	// The API-layer middleware decides what to do when the issuer hasn't
	// been updated to ADR 0011 yet.
	_ = json.Unmarshal(claimMap["tenant_id"], &c.tenantID)
	_ = json.Unmarshal(claimMap["principal_kind"], &c.principalKind)
	_ = json.Unmarshal(claimMap["scope"], &c.scope)
	_ = json.Unmarshal(claimMap["scope_kind"], &c.scopeKind)
	_ = json.Unmarshal(claimMap["scope_target_id"], &c.scopeTargetID)
	_ = json.Unmarshal(claimMap["client_fingerprint"], &c.clientFingerprint)

	return nil
}
