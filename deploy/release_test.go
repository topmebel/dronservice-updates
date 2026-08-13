package deploy

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

func TestReleasePublicKeyIsRSA3072(t *testing.T) {
	data, err := os.ReadFile("dronservice-release.pub")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key = %T, want RSA", parsed)
	}
	if key.N.BitLen() < 3072 {
		t.Fatalf("public key has %d bits, want at least 3072", key.N.BitLen())
	}
}

func TestReleaseWorkflowBuildsSignedARM64Artifact(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, fragment := range []string{
		"GOOS: linux",
		"GOARCH: arm64",
		"go test -race ./...",
		"DronService/internal/buildinfo.Version",
		"sha256sum",
		"RELEASE_SIGNING_PRIVATE_KEY",
		"openssl dgst -sha256 -sign",
		"gh release create",
	} {
		if !strings.Contains(workflow, fragment) {
			t.Errorf("release workflow does not contain %q", fragment)
		}
	}
}
