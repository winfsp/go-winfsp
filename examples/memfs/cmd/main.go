//go:build windows

package main

import (
	"os"
	"os/signal"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/gofs"
	"github.com/winfsp/go-winfsp/memfs"
)

var (
	mountpoint string = "X:"
)

var rootCmd = &cobra.Command{
	Use:   "memfs",
	Short: "Mount an In-Memory WinFSP filesystem",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Create and mount the filesystem.
		ptfs, err := winfsp.Mount(
			gofs.New(memfs.New()), mountpoint,
			winfsp.CaseSensitive(true),
		)
		if err != nil {
			return errors.Wrap(err, "mount filesystem")
		}
		defer ptfs.Unmount()

		// Keep running until the user interrupt.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		<-ch
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(
		&mountpoint, "mount", "m", mountpoint,
		"Where to mount the directory",
	)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
