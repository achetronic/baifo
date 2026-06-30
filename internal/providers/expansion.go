// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package providers

import (
	"fmt"
	"regexp"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/secrets"
)

// providerSecretRE matches the same ${secret:NAME} placeholder syntax
// the rest of baifo uses. Duplicated locally so this helper doesn't
// have to reach into the secrets package's private regex.
var providerSecretRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_\-]+)\}`)

// ExpandSecrets walks every ProviderEntry and substitutes
// ${secret:NAME} placeholders in api_key and headers values with the
// real secret. Returns a NEW slice; the input is not mutated so a
// concurrent ReloadFromDisk does not observe a half-expanded list.
//
// Behaviour mirrors the MCP header expansion:
//   - store nil (no secrets store wired): placeholders pass through.
//   - secret missing: returns a clear error so boot fails loudly
//     instead of silently sending an empty api_key to the provider.
//   - no placeholder: value copied verbatim.
//
// Why provider config gets early expansion, unlike tool args:
// providers are built ONCE at boot (and again on reload), and the
// resulting model client caches the api key inside its SDK. There's
// no per-call hook where we could expand later, so the substitution
// has to happen here, at config-load time.
func ExpandSecrets(in []config.ProviderEntry, store *secrets.Store) ([]config.ProviderEntry, error) {
	if len(in) == 0 {
		return in, nil
	}
	out := make([]config.ProviderEntry, len(in))
	for i, p := range in {
		expandedKey, err := expandSecretString(p.APIKey, store, fmt.Sprintf("providers[%s].api_key", p.Name))
		if err != nil {
			return nil, err
		}
		p.APIKey = expandedKey

		if len(p.Headers) > 0 {
			newHeaders := make(map[string]string, len(p.Headers))
			for k, v := range p.Headers {
				ev, err := expandSecretString(v, store, fmt.Sprintf("providers[%s].headers[%s]", p.Name, k))
				if err != nil {
					return nil, err
				}
				newHeaders[k] = ev
			}
			p.Headers = newHeaders
		}
		out[i] = p
	}
	return out, nil
}

// expandSecretString replaces every ${secret:NAME} occurrence in s
// with the real value. errorCtx is included in error messages so the
// user knows which field is the culprit.
func expandSecretString(s string, store *secrets.Store, errorCtx string) (string, error) {
	if !providerSecretRE.MatchString(s) {
		return s, nil
	}
	if store == nil {
		// No secrets store configured: leave the placeholder so the
		// downstream error ("api key is required") still happens and
		// hints that something needs configuring. We do NOT error
		// here because baifo can boot without secrets for dev.
		return s, nil
	}
	var firstErr error
	out := providerSecretRE.ReplaceAllStringFunc(s, func(match string) string {
		name := providerSecretRE.FindStringSubmatch(match)[1]
		value, err := store.Get(name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: secret %q: %w", errorCtx, name, err)
			}
			return match
		}
		return value
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
