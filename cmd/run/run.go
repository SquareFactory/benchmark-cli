package run

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/squarefactory/benchmark-api/benchmark"
	"github.com/squarefactory/benchmark-api/executor"
	"github.com/squarefactory/benchmark-api/scheduler"
	"github.com/squarefactory/benchmark-api/try"
	"github.com/urfave/cli/v2"
)

const (
	user = "root"
)

var flags = []cli.Flag{
	&cli.StringFlag{
		Name:  "container.path",
		Value: "/etc/hpl-benchmark/hpc-benchmarks:hpl.sqsh",
		EnvVars: []string{
			"CONTAINER_PATH",
		},
		Aliases: []string{"c"},
		Action: func(ctx *cli.Context, s string) error {
			info, err := os.Stat(s)
			if err != nil {
				return err
			}
			perms := info.Mode().Perm()
			if perms&0o077 != 0 {
				log.Fatal(
					"incorrect permissions for container .sqsh, must be user-only",
				)
			}
			return nil
		},
	},
}

var Command = &cli.Command{
	Name:      "run",
	Usage:     "Run an HPL-AI benchmark.",
	Flags:     flags,
	ArgsUsage: "<node_number>",
	Action: func(cCtx *cli.Context) error {

		ctx := cCtx.Context
		if cCtx.NArg() < 1 {
			return errors.New("not enough arguments")
		}

		arg := cCtx.Args().Get(0)
		node, err := strconv.Atoi(arg)
		if err != nil {
			log.Printf("Failed to convert %s to integer: %s", arg, err)
			return err
		}

		containerPath := os.Getenv("CONTAINER_PATH")
		workspace := filepath.Dir(containerPath)

		b := benchmark.NewBenchmark(
			benchmark.DATParams{},
			benchmark.SBATCHParams{
				Node:          node,
				ContainerPath: containerPath,
				Workspace:     workspace,
			},
			scheduler.NewSlurm(&executor.Shell{}, user),
		)

		files, err := b.GenerateFiles(ctx)
		if err != nil {
			log.Printf("Failed to generate benchmark files: %s", err)
			return err
		}

		if err := b.Run(ctx, &files); err != nil {
			log.Printf("Failed to run benchmark: %s", err)
			return err
		}

		jobID, err := try.Do(func() (int, error) {
			jobID, err := b.SlurmClient.FindRunningJobByName(
				ctx,
				&scheduler.FindRunningJobByNameRequest{
					Name: benchmark.JobName,
					User: benchmark.User,
				},
			)
			if err == nil {
				log.Print("benchmark is still running, unable to process results")
				return 0, errors.New("benchmark is still running")
			}

			return jobID, nil
		}, 60, 5*time.Minute)

		if err != nil {
			log.Printf("benchmark is still running, unable to process results")
			return err
		}
		log.Printf("benchmark finished running, processing results now")

		outputFile, err := b.SlurmClient.FindJobOutputFile(ctx, jobID)
		if err != nil {
			log.Printf("Unable to get outputFile path: %s", err)
			return err
		}

		cmd := exec.Command("python3", "process_output.py", outputFile)
		if err := cmd.Run(); err != nil {
			fmt.Println("Error: ", err)
			return err
		}

		return nil
	},
}
