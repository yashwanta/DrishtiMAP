package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// API keys are stored encrypted with Windows DPAPI: only the same Windows user on the same PC can read them.

func protect(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	b := []byte(plain)
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

func unprotect(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return "", fmt.Errorf("stored key is damaged")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("the stored key cannot be read by this Windows user; enter it again")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

// ProvidersView returns the provider list without keys.
func (a *App) ProvidersView() ([]Provider, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Provider, len(a.Cfg.Providers))
	for i, p := range a.Cfg.Providers {
		p.HasKey = p.KeyEnc != ""
		p.KeyEnc = ""
		out[i] = p
	}
	return out, a.Cfg.Active
}

// SaveProvider adds or updates a provider. key: "" keeps the stored key, "-" removes it.
func (a *App) SaveProvider(p Provider, key string, makeActive bool) error {
	p.ID = strings.TrimSpace(p.ID)
	if _, err := safeName(p.ID); err != nil {
		return err
	}
	if p.Kind != "anthropic" && p.Kind != "openai" {
		return fmt.Errorf("kind must be anthropic or openai")
	}
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if !strings.HasPrefix(p.BaseURL, "https://") && !strings.HasPrefix(p.BaseURL, "http://") {
		return fmt.Errorf("the address must start with https:// or http://")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := -1
	for i, o := range a.Cfg.Providers {
		if o.ID == p.ID {
			idx = i
			p.KeyEnc = o.KeyEnc
		}
	}
	switch key {
	case "":
	case "-":
		p.KeyEnc = ""
	default:
		enc, err := protect(strings.TrimSpace(key))
		if err != nil {
			return err
		}
		p.KeyEnc = enc
	}
	p.HasKey = false
	if idx >= 0 {
		a.Cfg.Providers[idx] = p
	} else {
		a.Cfg.Providers = append(a.Cfg.Providers, p)
	}
	if makeActive {
		a.Cfg.Active = p.ID
	}
	return a.saveCfg()
}

// DeleteProvider removes a provider.
func (a *App) DeleteProvider(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var keep []Provider
	for _, p := range a.Cfg.Providers {
		if p.ID != id {
			keep = append(keep, p)
		}
	}
	a.Cfg.Providers = keep
	if a.Cfg.Active == id {
		a.Cfg.Active = ""
	}
	return a.saveCfg()
}

// ActiveProvider returns the selected provider and its decrypted key.
func (a *App) ActiveProvider() (Provider, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.Cfg.Providers {
		if p.ID == a.Cfg.Active {
			key, err := unprotect(p.KeyEnc)
			if err != nil {
				return p, "", err
			}
			return p, key, nil
		}
	}
	return Provider{}, "", fmt.Errorf("no AI model is selected; open Settings and choose one")
}

// StoredKey returns the decrypted key saved for a provider ("" when none).
func (a *App) StoredKey(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.Cfg.Providers {
		if p.ID == id {
			k, _ := unprotect(p.KeyEnc)
			return k
		}
	}
	return ""
}
