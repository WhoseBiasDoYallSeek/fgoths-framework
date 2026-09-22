// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0

//go:build !darwin && !freebsd && !netbsd && !openbsd && !linux

package runtime

func setReusePort(fd uintptr) error {
	return nil
}