/*
Copyright 2026 KubeLease Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"sigs.k8s.io/yaml"
)

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func isMachineOutput(format string) bool {
	return format == "json" || format == "yaml"
}

func printObject(obj runtime.Object, format string) error {
	switch format {
	case "json":
		s := json.NewSerializerWithOptions(json.DefaultMetaFactory, nil, nil, json.SerializerOptions{Pretty: true})
		return s.Encode(obj, os.Stdout)
	case "yaml":
		s := json.NewSerializerWithOptions(json.DefaultMetaFactory, nil, nil, json.SerializerOptions{Yaml: true, Pretty: true})
		return s.Encode(obj, os.Stdout)
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

// EncodeYAML is a small helper for tests.
func EncodeYAML(obj runtime.Object) ([]byte, error) {
	return yaml.Marshal(obj)
}
