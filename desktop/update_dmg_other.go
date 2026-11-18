//go:build !darwin

package main

import "context"

func prepareNativeUpdate(context.Context, string, string) error { return nil }
