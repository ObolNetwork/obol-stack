//go:build darwin

package enclave

import (
	"errors"

	"golang.org/x/sys/unix"
)

// sysctlCsrActiveConfig reads kern.csr_active_config via sysctlbyname.
//
// Returns:
//   - (0, nil)   when SIP is fully active or the sysctl doesn't exist
//     (absence of kern.csr_active_config on Apple Silicon / macOS 26+
//     indicates SIP is enforced at the hardware level and cannot be altered).
//   - (val, nil) when the sysctl exists and SIP has been modified.
//   - (0, err)   on unexpected errors.
func sysctlCsrActiveConfig() (uint32, error) {
	val, err := unix.SysctlUint32("kern.csr_active_config")
	if err != nil {
		// ENOENT / ENODEV: the sysctl doesn't exist on this macOS version.
		// On Apple Silicon + macOS 26+ SIP is hardware-enforced; the absence
		// of this key means protections are fully active.
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENODEV) {
			return 0, nil
		}

		return 0, err
	}

	return val, nil
}
