package examples_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func TestExampleManifestsDecode(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(platformv1alpha1.AddToScheme(scheme))
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()

	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}

	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		found++
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		docs := splitYAMLDocuments(string(raw))
		if len(docs) == 0 {
			t.Fatalf("%s: no YAML documents", path)
		}
		for i, doc := range docs {
			obj, _, err := decoder.Decode([]byte(doc), nil, nil)
			if err != nil {
				t.Errorf("%s doc %d: decode: %v", path, i+1, err)
				continue
			}
			t.Logf("%s doc %d: %T", path, i+1, obj)
		}
	}
	if found == 0 {
		t.Fatal("no example YAML files found")
	}
}

func splitYAMLDocuments(s string) []string {
	parts := strings.Split(s, "\n---")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") && !strings.Contains(p, "\napiVersion:") {
			// skip comment-only; keep docs that include apiVersion after comments
			if !strings.Contains(p, "apiVersion:") {
				continue
			}
		}
		if strings.Contains(p, "apiVersion:") {
			out = append(out, p)
		}
	}
	return out
}
