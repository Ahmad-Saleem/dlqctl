package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "dlqctl",
	Short: "Dead-letter queue manager for SQS and Kafka",
}

func Execute() error {
	return rootCmd.Execute()
}
