// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package runtime

import "syscall"

// Linux exposes SO_REUSEPORT through x/sys/unix, but using the raw constant
// keeps the runtime dependency surface at stdlib-only. Value is from
// asm-generic/socket.h.
const linuxSOReusePort = 15

func setReusePort(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, linuxSOReusePort, 1)
}