//go:build !windows

package agentdir

import "os"

const testNoModes = "MECHBOX_TEST_NO_FILE_MODES"

func nativeModes() bool { return os.Getenv(testNoModes) == "" }
