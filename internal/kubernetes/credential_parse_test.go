package kubernetes

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"
)

var caOnce sync.Once
var testCA string

func fixtureCA(t *testing.T) string {
	t.Helper()
	caOnce.Do(func() {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			panic(e)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated-test-ca"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Unix(0, 0), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}
		raw, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if e != nil {
			panic(e)
		}
		testCA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}))
	})
	return testCA
}
func credentialInput(t *testing.T) CredentialInput {
	return CredentialInput{Name: "isolated test", Mode: "server_token", Context: "test", Server: "https://cluster.invalid:6443", CA: fixtureCA(t), Token: "synthetic-test-token-DO-NOT-LOG", Enabled: true}
}
func kubeInput(t *testing.T) CredentialInput {
	t.Helper()
	in := credentialInput(t)
	raw, _, e := normalizeCredential(in)
	if e != nil {
		t.Fatal(e)
	}
	return CredentialInput{Name: in.Name, Mode: "kubeconfig", Kubeconfig: string(raw), Enabled: true}
}
func TestCredentialFormatsAndNoSecretPreview(t *testing.T) {
	for _, in := range []CredentialInput{credentialInput(t), kubeInput(t)} {
		b, p, e := normalizeCredential(in)
		if e != nil {
			t.Fatal(e)
		}
		if len(b) > MaxCredentialBytes || p.Context != "test" || p.ClusterKey == "" {
			t.Fatal("normalization failed")
		}
		pub, _ := json.Marshal(p)
		if bytes.Contains(pub, []byte("synthetic-test-token")) || bytes.Contains(pub, []byte("BEGIN CERTIFICATE")) {
			t.Fatal("preview disclosed material")
		}
		var c kubeConfig
		if json.Unmarshal(b, &c) != nil || len(c.Users) != 1 || len(c.Clusters) != 1 || len(c.Contexts) != 1 {
			t.Fatal("not single context")
		}
	}
}
func TestStrictKubeconfigRejectsUnsafeInputs(t *testing.T) {
	base := kubeInput(t)
	cases := map[string]func(map[string]any){
		"exec": func(m map[string]any) {
			m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)["exec"] = map[string]any{"command": "echo"}
		},
		"auth-provider": func(m map[string]any) {
			m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)["auth-provider"] = map[string]any{"name": "gcp"}
		},
		"tokenFile": func(m map[string]any) {
			m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)["tokenFile"] = "/etc/token"
		},
		"external-key": func(m map[string]any) {
			m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)["client-key"] = "/tmp/key"
		},
		"external-ca": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["certificate-authority"] = "/etc/ca"
		},
		"proxy": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["proxy-url"] = "https://untrusted.invalid"
		},
		"skip-tls": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["insecure-skip-tls-verify"] = true
		},
		"unknown": func(m map[string]any) { m["unknown"] = true },
		"userinfo": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["server"] = "https://user:password@cluster.invalid"
		},
		"plaintext": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["server"] = "http://cluster.invalid"
		},
		"token-newline": func(m map[string]any) {
			m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)["token"] = "token\nheader"
		},
		"duplicate-name": func(m map[string]any) { m["contexts"] = append(m["contexts"].([]any), m["contexts"].([]any)[0]) },
		"preferences":    func(m map[string]any) { m["preferences"] = map[string]any{"colors": "true"} },
		"malformed-ca": func(m map[string]any) {
			m["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)["certificate-authority-data"] = base64.StdEncoding.EncodeToString([]byte("not a certificate"))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal([]byte(base.Kubeconfig), &m)
			change(m)
			b, _ := json.Marshal(m)
			in := base
			in.Kubeconfig = string(b)
			if _, e := PreviewCredential(in); e != ErrInvalid {
				t.Fatalf("expected rejection: %v", e)
			}
		})
	}
	for name, raw := range map[string]string{"duplicate-key": strings.Replace(base.Kubeconfig, `"kind":"Config"`, `"kind":"Config","kind":"Config"`, 1), "multiple-docs": base.Kubeconfig + "\n---\n{}", "yaml-alias": "a: &x {b: c}\nb: *x", "oversize": strings.Repeat("x", MaxImportBytes+1)} {
		t.Run(name, func(t *testing.T) {
			in := base
			in.Kubeconfig = raw
			if _, e := PreviewCredential(in); e != ErrInvalid {
				t.Fatalf("expected rejection: %v", e)
			}
		})
	}
}
func TestSelectionNormalizesOnlySelectedContext(t *testing.T) {
	in := kubeInput(t)
	var m map[string]any
	_ = json.Unmarshal([]byte(in.Kubeconfig), &m)
	m["contexts"] = append(m["contexts"].([]any), map[string]any{"name": "second", "context": map[string]any{"cluster": "cluster", "user": "identity", "namespace": "ignored"}})
	b, _ := json.Marshal(m)
	in.Kubeconfig = string(b)
	in.Context = "second"
	raw, p, e := normalizeCredential(in)
	if e != nil || p.Context != "second" {
		t.Fatal("selected context failed", e)
	}
	if bytes.Contains(raw, []byte("ignored")) {
		t.Fatal("namespace escaped grant scope")
	}
	in.Context = "missing"
	if _, e = PreviewCredential(in); e != ErrInvalid {
		t.Fatal("missing context accepted")
	}
}
func TestProfileAndReferenceBoundaries(t *testing.T) {
	profile := testProfile()
	if validateProfile(profile) != nil {
		t.Fatal("fixture")
	}
	for name, change := range map[string]func(*ToolProfile){"tag": func(p *ToolProfile) { p.Image = "registry.invalid/kubectl:latest" }, "root": func(p *ToolProfile) { p.UID = 0 }, "host-network": func(p *ToolProfile) { p.Network = "host" }, "shell-path": func(p *ToolProfile) { p.KubectlPath = "/bin/kubectl;evil" }, "relative": func(p *ToolProfile) { p.KubectlPath = "kubectl" }, "protected-registry": func(p *ToolProfile) { p.RegistryCredentialID = "kcred_test" }} {
		t.Run(name, func(t *testing.T) {
			p := profile
			change(&p)
			if validateProfile(p) == nil {
				t.Fatal("unsafe profile accepted")
			}
		})
	}
	if ValidateGenericCredentialReference("kcred_test") != ErrDenied || ValidateGenericCredentialReference("old-credential") != nil {
		t.Fatal("namespace restriction")
	}
}

func TestEndpointAndCABundleCanonicalIdentity(t *testing.T) {
	in := credentialInput(t)
	in.Server = "https://CLUSTER.invalid:443/"
	a, e := PreviewCredential(in)
	if e != nil {
		t.Fatal(e)
	}
	in.Server = "https://cluster.invalid"
	in.CA += in.CA
	b, e := PreviewCredential(in)
	if e != nil || a.ClusterKey != b.ClusterKey {
		t.Fatal("equivalent trust split cluster identity", e)
	}
	in.Server = "https://cluster.invalid:99999"
	if _, e = PreviewCredential(in); e != ErrInvalid {
		t.Fatal("invalid port accepted")
	}
	if !validDeploymentName("service.example") || validDeploymentName("service..example") || validName("app.example", 63) {
		t.Fatal("DNS subdomain/label mismatch")
	}
}

func TestEmbeddedClientCertificateAcceptedButMixedAuthRejected(t *testing.T) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "test-client"}, NotBefore: time.Unix(0, 0), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyder, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	cert := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	secret := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyder}))
	in := kubeInput(t)
	var m map[string]any
	_ = json.Unmarshal([]byte(in.Kubeconfig), &m)
	u := m["users"].([]any)[0].(map[string]any)["user"].(map[string]any)
	delete(u, "token")
	u["client-certificate-data"] = cert
	u["client-key-data"] = secret
	b, _ := json.Marshal(m)
	in.Kubeconfig = string(b)
	if _, e = PreviewCredential(in); e != nil {
		t.Fatal("embedded client pair rejected", e)
	}
	u["token"] = "fake-token"
	b, _ = json.Marshal(m)
	in.Kubeconfig = string(b)
	if _, e = PreviewCredential(in); e != ErrInvalid {
		t.Fatal("mixed auth accepted", e)
	}
}
