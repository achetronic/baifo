// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package terminal

// prepareUTF8 is a no-op on every non-Windows platform: POSIX
// terminals negotiate encoding through the locale environment and
// baifo's output is UTF-8 end to end already.
func prepareUTF8() (restore func(), err error) {
	return func() {}, nil
}
