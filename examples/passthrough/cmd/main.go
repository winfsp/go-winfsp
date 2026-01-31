//go:build windows

package main

import (
	"os"
	"os/signal"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/winfsp/go-winfsp"
	"github.com/winfsp/go-winfsp/examples/passthrough"
	"github.com/winfsp/go-winfsp/gofs"
)

var (
	mountpoint string = "X:"
)

var rootCmd = &cobra.Command{
	Use:   "passthrough",
	Short: "Mount a directory as WinFSP filesystem",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := args[0]

		// Open and pin the directory first.
		f, err := os.Open(dir)
		if err != nil {
			return errors.Wrapf(err, "open dir %q", dir)
		}
		defer f.Close()

		stat, err := f.Stat()
		if err != nil {
			return errors.Wrapf(err, "stat dir %q", dir)
		}
		if !stat.IsDir() {
			return errors.Errorf("path %q is not a directory", dir)
		}

		// Create and mount the filesystem.
		ptfs, err := winfsp.Mount(gofs.New(&passthrough.Passthrough{
			Dir: dir,
		}), mountpoint)
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
