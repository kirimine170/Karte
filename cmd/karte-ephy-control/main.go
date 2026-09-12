// karte-ephy-control is a local human administration command．It is never
// exposed as a Runtime model tool or a proposal operation．
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"karte/internal/canonical"
	"karte/internal/ephyrecordsv2"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("karte-ephy-control", flag.ContinueOnError)
	root := f.String("data-root", "", "Karte data root")
	config := f.String("config-root", "", "Private registration directory outside the data root and Git")
	grantPath := f.String("grant", "", "Human-reviewed grant JSON")
	credentialPath := f.String("producer-credential", "", "Private producer credential JSON；created if absent")
	privacyPath := f.String("privacy-policy", "", "Current shared privacy policy JSON")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *root == "" || f.NArg() != 1 {
		return fmt.Errorf("usage: karte-ephy-control -data-root DIR [options] configure|privacy|capabilities|process|recover")
	}
	s, e := ephyrecordsv2.New(*root, *config)
	if e != nil {
		return e
	}
	var result any
	switch f.Arg(0) {
	case "configure":
		if *grantPath == "" || *credentialPath == "" {
			return fmt.Errorf("configure requires -grant and -producer-credential")
		}
		raw, e := os.ReadFile(*grantPath)
		if e != nil {
			return e
		}
		g, e := ephyrecordsv2.DecodeGrant(raw)
		if e != nil {
			return e
		}
		path, e := filepath.Abs(*credentialPath)
		if e != nil {
			return e
		}
		if e = ephyrecordsv2.ValidateCredentialDirectory(s.DataRoot, filepath.Dir(path)); e != nil {
			return e
		}
		var key []byte
		if raw, e := os.ReadFile(path); e == nil {
			st, e := os.Lstat(path)
			if e != nil || !st.Mode().IsRegular() || canonical.CheckPrivateFile(path) != nil || canonical.CheckPrivateDirectory(filepath.Dir(path)) != nil {
				return fmt.Errorf("unsafe_producer_credential")
			}
			credential, e := ephyrecordsv2.DecodeProducerCredential(raw)
			if e != nil || credential.ProducerID != g.ProducerID || credential.KeyID != g.KeyID {
				return fmt.Errorf("credential_identity_mismatch")
			}
			key, e = hex.DecodeString(credential.Key)
			if e != nil {
				return e
			}
		} else if os.IsNotExist(e) {
			key = make([]byte, 32)
			if _, e = rand.Read(key); e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				return e
			}
			if e = canonical.SecureDirectory(filepath.Dir(path)); e != nil {
				return e
			}
			raw, e := json.Marshal(ephyrecordsv2.ProducerCredential{ProducerID: g.ProducerID, KeyID: g.KeyID, Key: hex.EncodeToString(key)})
			if e != nil {
				return e
			}
			if e = canonical.WithWriter(filepath.Dir(path), func(w *canonical.Writer) error { return w.WriteCAS(filepath.Base(path), nil, raw, 0600) }); e != nil {
				return e
			}
		} else {
			return e
		}
		if e = s.Configure(g, key); e != nil {
			return e
		}
		result = map[string]any{"status": "configured", "scope_id": g.ScopeID, "policy_revision": g.Revision, "enabled": g.Enabled}
	case "privacy":
		if *privacyPath == "" {
			return fmt.Errorf("privacy requires -privacy-policy")
		}
		raw, e := os.ReadFile(*privacyPath)
		if e != nil {
			return e
		}
		p, e := ephyrecordsv2.DecodePrivacyPolicy(raw)
		if e != nil {
			return e
		}
		if e = s.SetPrivacyPolicy(p); e != nil {
			return e
		}
		result = map[string]string{"status": "privacy_policy_updated"}
	case "capabilities":
		result, e = s.Capabilities()
	case "process":
		result, e = s.ProcessPending(20)
	case "recover":
		result, e = s.Recover()
	default:
		return fmt.Errorf("unsupported_control_operation")
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
