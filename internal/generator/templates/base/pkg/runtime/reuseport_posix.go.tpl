// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0

//go:build darwin || freebsd || netbsd || openbsd

package runtime

import "syscall"

func setReusePort(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
}