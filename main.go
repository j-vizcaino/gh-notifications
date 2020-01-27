package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/google/go-github/v29/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

type Filters struct {
	repository        string
	subjectType       string
	subjectState      string
	listRead          bool
	unsubscribeUnread bool
}

var (
	rootCommand *cobra.Command
	filters     = Filters{}
)

func init() {
	initCommands()
}

func initCommands() {
	rootCommand = &cobra.Command{}
	globalFlags := rootCommand.PersistentFlags()

	globalFlags.StringVar(&filters.repository, "repo", "", "Consider this repository only. Exampe: org/reponame")
	globalFlags.StringVar(&filters.subjectType, "type", "PullRequest", "Notifications for this type of subject only. Supported options: PullRequest or Issue")

	list := &cobra.Command{
		Use:   "list",
		Short: "List notifications",
		Run:   runList,
	}

	flags := list.Flags()
	flags.StringVar(&filters.subjectState, "state", "", "Act on notifications where the subject is in that state. Supported options: open, closed and merged. Merged is for PR only")
	flags.BoolVar(&filters.listRead, "show-read", false, "Show read notifications")

	unsubscribe := &cobra.Command{
		Use:   "unsubscribe",
		Short: "Unsubscribe from the notifications matching the filters",
		Run:   runUnsubscribe,
	}
	flags = unsubscribe.Flags()
	flags.StringVar(&filters.subjectState, "state", "closed", "Act on notifications where the subject is in that state. Supported options: open, closed and merged. Merged is for PR only")
	flags.BoolVar(&filters.unsubscribeUnread, "unread", false, "Unsubscribe from unread notifications")

	rootCommand.AddCommand(list, unsubscribe)
}

func mustGHClient() *github.Client {
	apiToken := os.Getenv("GITHUB_TOKEN")
	if apiToken == "" {
		fmt.Println("Please provide an API token using the GITHUB_TOKEN env var.")
		os.Exit(1)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: apiToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)
	return client
}

func runList(_ *cobra.Command, _ []string) {
	gh := mustGHClient()

	printNotification := func(n *github.Notification) error {
		state := filters.subjectState
		if state == "" {
			var err error
			state, err = resolveNotificationSubjectState(gh, n)
			if err != nil {
				return err
			}
		}
		fmt.Printf("%-80s %s\n", n.GetSubject().GetTitle(), state)
		return nil
	}

	err := forEachNotifications(gh, &github.NotificationListOptions{All: filters.listRead}, printNotification)
	if err != nil {
		fmt.Printf("Failed to process notifications, %s", err)
	}
}

func runUnsubscribe(_ *cobra.Command, _ []string) {
	gh := mustGHClient()

	unsubscribe := func(n *github.Notification) error {
		if n.GetUnread() {
			if !filters.unsubscribeUnread {
				return nil
			}
			if _, err := gh.Activity.MarkThreadRead(context.TODO(), n.GetID()); err != nil {
				return fmt.Errorf("failed to mark thread as read, %w", err)
			}
		}
		_, err := gh.Activity.DeleteThreadSubscription(context.TODO(), n.GetID())
		if err != nil {
			return fmt.Errorf("failed to unsubscribe from thread, %w", err)
		}
		subject := n.GetSubject()
		fmt.Printf("✅  %s (thread %s, reason was %q, %s)\n", subject.GetTitle(), n.GetID(), n.GetReason(), subject.GetURL())
		return nil
	}

	err := forEachNotifications(gh, &github.NotificationListOptions{All: true}, unsubscribe)
	if err != nil {
		fmt.Printf("Failed to process notifications, %s", err)
	}
}

func forEachNotifications(client *github.Client, opts *github.NotificationListOptions, do func(*github.Notification) error) error {
	notifications, _, err := client.Activity.ListNotifications(context.TODO(), opts)
	if err != nil {
		return fmt.Errorf("failed to list notifications, %w", err)
	}

	for _, notif := range notifications {
		subject := notif.GetSubject()
		if filters.subjectType != subject.GetType() {
			continue
		}

		// TODO: use the dedicated repository endpoint to play nice with the API
		if filters.repository != "" && filters.repository != notif.GetRepository().GetFullName() {
			continue
		}

		if filters.subjectState != "" {
			state, err := resolveNotificationSubjectState(client, notif)
			if err != nil {
				return err
			}
			if state != filters.subjectState {
				continue
			}
		}
		if err := do(notif); err != nil {
			return err
		}
	}
	return nil
}

func resolveNotificationSubjectState(client *github.Client, n *github.Notification) (string, error) {
	var state string

	subject := n.GetSubject()
	switch subject.GetType() {
	case "PullRequest":
		var pr github.PullRequest
		if err := getObject(client, subject.GetURL(), &pr); err != nil {
			return "", fmt.Errorf("failed to get pull request details, %w", err)
		}
		state = pr.GetState()
		if pr.GetMerged() {
			state = "merged"
		}
	case "Issue":
		var issue github.Issue
		if err := getObject(client, subject.GetURL(), &issue); err != nil {
			return "", fmt.Errorf("failed to get issue details, %w", err)
		}
		state = issue.GetState()
	default:
		return "", fmt.Errorf("unhandled subject type %q", subject.GetType())
	}
	return state, nil
}

func getObject(client *github.Client, url string, out interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	_, err = client.Do(context.TODO(), req, out)
	return err
}

func main() {
	err := rootCommand.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
