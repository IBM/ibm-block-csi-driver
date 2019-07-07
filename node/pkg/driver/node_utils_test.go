package driver

import (
	"fmt"
	"os"
	//"path/filepath"
	//"strings"
	"io/ioutil"
	"syscall"
	"testing"
)

var (
	nodeUtils = NewNodeUtils()
)

func TestParseIscsiInitiators(t *testing.T) {
	testCases := []struct {
		name         string
		file_content string
		expErr       error
		expIqn       string
	}{
		{
			name:         "wrong iqn file",
			file_content: "wrong-content",
			expErr:       fmt.Errorf(ErrorWhileTryingToReadIQN, "wrong-content"),
		},
		{
			name:   "non existing file",
			expErr: &os.PathError{"open", "/non/existent/path", syscall.ENOENT},
		},
		{
			name:         "right_iqn",
			file_content: "InitiatorName=iqn.1996-05.com.redhat:123123122",
			expIqn:       "iqn.1996-05.com.redhat:123123122",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			filePath := ""

			if tc.file_content != "" {
				tmpFile, err := ioutil.TempFile(os.TempDir(), "iscis-initiators-")
				fmt.Println(tmpFile)
				if err != nil {
					t.Fatalf("Cannot create temporary file : %v", err)
				}

				defer os.Remove(tmpFile.Name())

				fmt.Println("Created File: " + tmpFile.Name())

				text := []byte(tc.file_content)
				if _, err = tmpFile.Write(text); err != nil {
					t.Fatalf("Failed to write to temporary file: %v", err)
				}

				if err := tmpFile.Close(); err != nil {
					t.Fatalf(err.Error())
				}
				filePath = tmpFile.Name()
			} else {
				filePath = "/non/existent/path"
			}

			isci, err := nodeUtils.ParseIscsiInitiators(filePath)

			if tc.expErr != nil {
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expecting err: expected %v, got %v", tc.expErr, err)
				}

			} else {
				if err != nil {
					t.Fatalf("err is not nil. got: %v", err)
				}
				if isci != tc.expIqn {
					t.Fatalf("scheme mismatches: expected %v, got %v", tc.expIqn, isci)
				}

			}

		})
	}

}
