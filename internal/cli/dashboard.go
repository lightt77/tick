package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/lightt77/tick/internal/daemon"
	"github.com/lightt77/tick/internal/dashboard"
	"github.com/lightt77/tick/internal/paths"
)

// staleAfter is how old the daemon's data file may be before `tick dashboard`
// stops trusting it (an old daemon binary never writes one).
const staleAfter = 90 * time.Second

func (a *app) dashboardCmd() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Open the read-only dashboard page in your browser",
		Long: `Copies the dashboard page into tick's data folder and opens it. The running
daemon keeps the page's data file fresh (every change, plus a heartbeat), and the
page refreshes itself. With no daemon running, this writes one snapshot instead and
the page says so.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := a.store() // also creates the data folder
			if err != nil {
				return err
			}
			if err := dashboard.InstallPage(paths.Dashboard()); err != nil {
				return err
			}
			running, _ := daemon.Running(paths.DaemonLock())
			info, statErr := os.Stat(paths.DashboardData())
			if !(running && statErr == nil && time.Since(info.ModTime()) < staleAfter) {
				if err := dashboard.Snapshot(s, paths.RunLog(), paths.DashboardData(), time.Now()); err != nil {
					return err
				}
			}
			fmt.Fprintln(a.out, paths.Dashboard())
			if noOpen {
				return nil
			}
			return exec.Command("open", paths.Dashboard()).Run()
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "only write the files and print the page's path")
	return cmd
}
