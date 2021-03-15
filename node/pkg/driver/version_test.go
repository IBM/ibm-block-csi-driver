/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func getConfigFilePath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "../../../", "common", "config.yaml")
	return dir, nil
}

func TestGetVersion(t *testing.T) {

	dir, err := getConfigFilePath()
	if err != nil {
		t.Fatalf("Getting config file returned an error")
	}

	fmt.Println(dir)

	version, err := GetVersion(dir)

	expected := VersionInfo{
		DriverVersion: "1.6.0",
		GitCommit:     "",
		BuildDate:     "",
		GoVersion:     runtime.Version(),
		Compiler:      runtime.Compiler,
		Platform:      fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}

	if !reflect.DeepEqual(version, expected) {
		t.Fatalf("structs not equall\ngot:\n%+v\nexpected:\n%+v", version, expected)
	}

	if err != nil {
		t.Fatalf("Get version return error")
	}
}

func TestGetVersionJSON(t *testing.T) {
	dir, err := getConfigFilePath()

	if err != nil {
		t.Fatalf("Getting config file returned an error")
	}

	version, err := GetVersionJSON(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := fmt.Sprintf(`{
  "driverVersion": "1.6.0",
  "gitCommit": "",
  "buildDate": "",
  "goVersion": "%s",
  "compiler": "%s",
  "platform": "%s"
}`, runtime.Version(), runtime.Compiler, fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

	if version != expected {
		t.Fatalf("json not equall\ngot:\n%s\nexpected:\n%s", version, expected)
	}
}
