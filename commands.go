package dstask

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v2"
	"mvdan.cc/xurls/v2"
)

// CommandAdd adds a new task to the task database.
func CommandAdd(conf Config, ctx, query Query) error {
	if query.Text == "" && query.Template == 0 {
		return errors.New("task description or template required")
	}
	if query.DateFilter != "" && query.DateFilter != "in" && query.DateFilter != "on" {
		return errors.New("cannot use date filter with add command")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if query.Template > 0 {
		var taskSummary string

		tt := ts.MustGetByID(query.Template)
		query = query.Merge(ctx)

		if query.Text != "" {
			taskSummary = query.Text
		} else {
			taskSummary = tt.Summary
		}

		// create task from template task tt
		task := Task{
			WritePending: true,
			Status:       STATUS_PENDING,
			Summary:      taskSummary,
			Tags:         tt.Tags,
			Project:      tt.Project,
			Priority:     tt.Priority,
			Due:          tt.Due,
			Notes:        tt.Notes,
		}

		// Modify the task with any tags/projects/antiProjects/priorities/dueDates in query
		task.Modify(query)

		task = ts.MustLoadTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Added %s", task)

		if tt.Status != STATUS_TEMPLATE {
			// Insert Text Statement to inform user of real Templates
			fmt.Print(
				"\nYou've copied an open task!\nTo learn more about creating templates enter 'dstask help template'\n\n",
			)
		}
	} else if query.Text != "" {
		ctx.PrintContextDescription()
		query = query.Merge(ctx)
		task := Task{
			WritePending: true,
			Status:       STATUS_PENDING,
			Summary:      query.Text,
			Tags:         query.Tags,
			Project:      query.Project,
			Priority:     query.Priority,
			Due:          query.Due,
			Notes:        query.Note,
		}
		task = ts.MustLoadTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Added %s", task)
	}

	return nil
}

// CommandContext sets a global context for dstask.
func CommandContext(conf Config, state State, ctx, query Query) error {
	if len(os.Args) < 3 {
		fmt.Println(ctx)
	} else if os.Args[2] == "none" {
		if err := state.SetContext(Query{}); err != nil {
			return err
		}
	} else {
		if err := state.SetContext(query); err != nil {
			return err
		}
	}

	state.Save(conf.StateFile)

	return nil
}

// uuidMutationTarget resolves one open UUID subject directly from the task set
// and removes it from query text, avoiding the concurrency-unstable numeric ID.
func uuidMutationTarget(ts *TaskSet, query Query) (Task, Query, bool, error) {
	target, remainder, _ := strings.Cut(query.Text, " ")
	if !IsValidUUID4String(target) {
		return Task{}, query, false, nil
	}
	task := ts.tasksByUUID[target]
	if task == nil || task.Status == STATUS_RESOLVED {
		return Task{}, query, true, fmt.Errorf("no open task with UUID %s exists", target)
	}
	query.Text = remainder
	return *task, query, true, nil
}

// normalizeMutationText moves text after the literal-note separator into the
// mutation text field. Mixing text before and after the separator is ambiguous.
func normalizeMutationText(query Query) (Query, error) {
	if query.Note == "" {
		return query, nil
	}
	if query.Text != "" {
		return query, errors.New("mutation text cannot appear both before and after /")
	}
	query.Text = query.Note
	return query, nil
}

// CommandDone marks a task as done.
func CommandDone(conf Config, ctx, query Query) error {
	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	var tasks []Task
	if len(query.IDs) > 0 {
		for _, id := range query.IDs {
			tasks = append(tasks, ts.MustGetByID(id))
		}
	} else {
		var found bool
		var task Task
		task, query, found, err = uuidMutationTarget(ts, query)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no ID(s) or UUID specified")
		}
		tasks = append(tasks, task)
	}

	query, err = normalizeMutationText(query)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		task.Status = STATUS_RESOLVED
		task.Resolved = time.Now()

		if query.Text != "" {
			task.Notes += "\n" + query.Text
		}

		ts.MustUpdateTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Resolved %s", task)
	}

	return nil
}

// CommandEdit edits a task's metadata, such as status, projects, tags, etc.
func CommandEdit(conf Config, ctx, query Query) error {
	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	if len(query.IDs) == 0 {
		return errors.New("no ID(s) specified")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	for _, id := range query.IDs {
		task := ts.MustGetByID(id)
		data, err := yaml.Marshal(&task)
		if err != nil {
			// TODO present error to user, specific error message is important
			return fmt.Errorf("failed to marshal task %s", task)
		}

		for {
			edited := MustEditBytes(data, MakeTempFilename(task.ID, task.Summary, "yml"))

			err = yaml.Unmarshal(edited, &task)
			if err == nil {
				break
			} else {
				// edit is a special case that won't be used as part of an API,
				// so it's OK to exit
				ConfirmOrAbort("Failed to unmarshal %s\nTry again?", err)
			}
		}

		ts.MustUpdateTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Edited %s", task)
	}

	return nil
}

// CommandHelp prints for a specific command or all commands.
func CommandHelp(args []string) {
	if len(os.Args) > 2 {
		Help(os.Args[2])
	} else {
		Help("")
	}
}

// CommandLog logs a completed task immediately. Useful for tracking tasks after
// they're already completed.
func CommandLog(conf Config, ctx, query Query) error {
	if query.Text == "" {
		return errors.New("task description required")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	ctx.PrintContextDescription()
	query = query.Merge(ctx)
	task := Task{
		WritePending: true,
		Status:       STATUS_RESOLVED,
		Summary:      query.Text,
		Tags:         query.Tags,
		Project:      query.Project,
		Priority:     query.Priority,
		Due:          query.Due,
		Resolved:     time.Now(),
	}
	task = ts.MustLoadTask(task)
	ts.SavePendingChanges()
	MustGitCommit(conf.Repo, "Logged %s", task)

	return nil
}

// CommandModify applies a change to tasks specified by ID, or all tasks in
// current context.
func CommandModify(conf Config, ctx, query Query) error {
	if !query.HasOperators() {
		return errors.New("no operations specified")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if len(query.IDs) == 0 {
		ts.Filter(ctx)

		if StdoutIsTTY() {
			ConfirmOrAbort(
				"no IDs specified. Apply to all %d tasks in current ctx?",
				len(ts.Tasks()),
			)
		}

		for _, task := range ts.Tasks() {
			task.Modify(query)
			ts.MustUpdateTask(task)
			ts.SavePendingChanges()
			MustGitCommit(conf.Repo, "Modified %s", task)
		}
	} else {
		for _, id := range query.IDs {
			task := ts.MustGetByID(id)
			task.Modify(query)
			ts.MustUpdateTask(task)
			ts.SavePendingChanges()
			MustGitCommit(conf.Repo, "Modified %s", task)
		}
	}

	return nil
}

// CommandNext prints the unresolved tasks associated with the current context.
// This is the default command.
func CommandNext(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if len(query.IDs) > 0 {
		// addressing task by ID, ignores context
		if query.HasOperators() {
			return errors.New("operators not valid when addressing task by ID")
		}
	} else {
		// apply context
		query = query.Merge(ctx)
	}

	ts.Filter(query)
	if err := ts.DisplayByNext(ctx, true); err != nil {
		return err
	}

	return nil
}

// CommandNote edits or prints the markdown note associated with the task.
func CommandNote(conf Config, ctx, query Query) error {
	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	var tasks []Task
	if len(query.IDs) > 0 {
		for _, id := range query.IDs {
			tasks = append(tasks, ts.MustGetByID(id))
		}
	} else {
		var found bool
		var task Task
		task, query, found, err = uuidMutationTarget(ts, query)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no ID(s) or UUID specified")
		}
		tasks = append(tasks, task)
	}

	query, err = normalizeMutationText(query)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		// If stdout is a TTY, we may open the editor
		if StdoutIsTTY() {
			if query.Text == "" {
				task.Notes = string(
					MustEditBytes(
						[]byte(task.Notes),
						MakeTempFilename(task.ID, task.Summary, "md"),
					),
				)
			} else {
				if task.Notes == "" {
					task.Notes = query.Text
				} else {
					task.Notes += "\n" + query.Text
				}
			}

			ts.MustUpdateTask(task)
			ts.SavePendingChanges()
			MustGitCommit(conf.Repo, "Edit note %s", task)
		} else {
			// If stdout is not a TTY, we simply write markdown notes to stdout
			if err := WriteStdout([]byte(task.Notes)); err != nil {
				ExitFail("Could not write to stdout: %v", err)
			}
		}
	}

	return nil
}

// CommandOpen opens a task URL in the browser, if the task has a URL.
func CommandOpen(conf Config, ctx, query Query) error {
	if len(query.IDs) == 0 {
		return errors.New("no ID(s) specified")
	}

	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	for _, id := range query.IDs {
		task := ts.MustGetByID(id)
		urls := xurls.Relaxed().FindAllString(task.Summary+" "+task.Notes, -1)

		if len(urls) == 0 {
			return fmt.Errorf("no URLs found in task %v", task.ID)
		}

		for _, url := range urls {
			MustOpenBrowser(url)
		}
	}

	return nil
}

// CommandRemove removes a task by ID from the database.
func CommandRemove(conf Config, ctx, query Query) error {
	if len(query.IDs) == 0 {
		return errors.New("no ID(s) specified")
	}

	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	for _, id := range query.IDs {
		task := ts.MustGetByID(id)
		fmt.Println(task)
	}

	if StdoutIsTTY() {
		ConfirmOrAbort(
			"\nThe above %d task(s) will be deleted without checking subtasks. Continue?",
			len(query.IDs),
		)
	}

	for _, id := range query.IDs {
		task := ts.MustGetByID(id)
		// Mark our task for deletion
		task.Deleted = true

		// MustUpdateTask validates and normalises our task object
		ts.MustUpdateTask(task)
		ts.SavePendingChanges()

		if query.Text != "" {
			// commit comment, put in body
			MustGitCommit(conf.Repo, "Removed: %s\n\n%s", task, query.Text)
		} else {
			MustGitCommit(conf.Repo, "Removed: %s", task)
		}
	}

	return nil
}

// CommandShowActive prints a list of active tasks.
func CommandShowActive(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	query = query.Merge(ctx)
	ts.Filter(query)
	ts.FilterByStatus(STATUS_ACTIVE)
	if err := ts.DisplayByNext(ctx, true); err != nil {
		return err
	}

	return nil
}

// CommandShowProjects prints a list of projects associated with all tasks.
// Ignores context/query for valid output.
func CommandShowProjects(conf Config, ctx, query Query) error {
	if len(query.IDs) > 0 || query.HasOperators() {
		return errors.New("query/context not supported for show-projects")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, true)
	if err != nil {
		return err
	}

	if err := ts.DisplayProjects(); err != nil {
		return err
	}

	return nil
}

// CommandShowOpen prints a list of open tasks without truncation.
func CommandShowOpen(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	query = query.Merge(ctx)
	ts.Filter(query)
	if err := ts.DisplayByNext(ctx, false); err != nil {
		return err
	}

	return nil
}

// CommandShowPaused prints a list of paused tasks.
func CommandShowPaused(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	query = query.Merge(ctx)
	ts.Filter(query)
	ts.FilterByStatus(STATUS_PAUSED)
	if err := ts.DisplayByNext(ctx, true); err != nil {
		return err
	}

	return nil
}

// CommandShowResolved prints a list of resolved tasks.
func CommandShowResolved(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, true)
	if err != nil {
		return err
	}

	query = query.Merge(ctx)

	ts.UnHide()
	ts.Filter(query)
	ts.FilterByStatus(STATUS_RESOLVED)
	ts.DisplayByWeek()

	return nil
}

// CommandShowTags prints a list of all tags associated with non-resolved tasks.
func CommandShowTags(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	query = query.Merge(ctx)
	ts.Filter(query)

	for tag := range ts.GetTags() {
		fmt.Println(tag)
	}

	return nil
}

// CommandShowTemplates show a list of task templates.
func CommandShowTemplates(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	ts.UnHide()
	ts.FilterByStatus(STATUS_TEMPLATE)

	query = query.Merge(ctx)
	ts.Filter(query)
	if err := ts.DisplayByNext(ctx, true); err != nil {
		return err
	}

	return nil
}

// CommandShowUnorganised prints a list of tasks without tags or projects.
// no context / query valid.
func CommandShowUnorganised(conf Config, ctx, query Query) error {
	if len(query.IDs) > 0 || query.HasOperators() {
		return errors.New("query/context not used for show-unorganised")
	}

	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	ts.FilterOrganised()
	if err := ts.DisplayByNext(ctx, true); err != nil {
		return err
	}

	return nil
}

// CommandStart marks an existing task as started, by ID. If no ID is
// specified, it creates a new task and starts it.
func CommandStart(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if query.Template > 0 {
		return errors.New("templates not yet supported for start command")
	}

	uuidTask := Task{}
	uuidTarget := false
	if len(query.IDs) == 0 {
		uuidTask, query, uuidTarget, err = uuidMutationTarget(ts, query)
		if err != nil {
			return err
		}
	}

	if len(query.IDs) > 0 || uuidTarget {
		var tasks []Task
		if uuidTarget {
			tasks = append(tasks, uuidTask)
		} else {
			for _, id := range query.IDs {
				tasks = append(tasks, ts.MustGetByID(id))
			}
		}
		for _, task := range tasks {
			task.Status = STATUS_ACTIVE

			if identity := os.Getenv("DSTASK_IDENTITY"); identity != "" {
				task.DelegatedTo = identity
			}

			if query.Text != "" {
				task.Notes += "\n" + query.Text
			}

			ts.MustUpdateTask(task)

			ts.SavePendingChanges()
			MustGitCommit(conf.Repo, "Started %s", task)

			if task.Notes != "" {
				fmt.Printf("\nNotes on task %d:\n\033[38;5;245m%s\033[0m\n\n", task.ID, task.Notes)
			}
		}
	} else if query.Text != "" {
		// create a new task that is already active (started)
		query = query.Merge(ctx)
		task := Task{
			WritePending: true,
			Status:       STATUS_ACTIVE,
			Summary:      query.Text,
			Tags:         query.Tags,
			Project:      query.Project,
			Priority:     query.Priority,
			Notes:        query.Note,
			DelegatedTo:  os.Getenv("DSTASK_IDENTITY"),
		}
		task = ts.MustLoadTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Added and started %s", task)
	} else {
		return errors.New("nothing to do -- specify an ID or describe a task")
	}

	return nil
}

// CommandStop marks a task as stopped.
func CommandStop(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if query.HasOperators() {
		return errors.New("operators not valid in this context")
	}

	var tasks []Task
	if len(query.IDs) > 0 {
		for _, id := range query.IDs {
			tasks = append(tasks, ts.MustGetByID(id))
		}
	} else {
		var found bool
		var task Task
		task, query, found, err = uuidMutationTarget(ts, query)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no ID(s) or UUID specified")
		}
		tasks = append(tasks, task)
	}

	for _, task := range tasks {
		task.Status = STATUS_PAUSED

		if query.Text != "" {
			task.Notes += "\n" + query.Text
		}

		ts.MustUpdateTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Stopped %s", task)
	}

	return nil
}

// CommandSync pushes and pulls task database changes from the remote repository.
func CommandSync(repoPath string) error {
	// TODO(dontlaugh) return error
	Sync(repoPath)

	return nil
}

// CommandTemplate creates a new task template.
func CommandTemplate(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, false)
	if err != nil {
		return err
	}

	if len(query.IDs) > 0 {
		for _, id := range query.IDs {
			task := ts.MustGetByID(id)
			task.Status = STATUS_TEMPLATE

			ts.MustUpdateTask(task)
			ts.SavePendingChanges()
			MustGitCommit(conf.Repo, "Changed %s to Template", task)
		}
	} else if query.Text != "" {
		query = query.Merge(ctx)
		task := Task{
			WritePending: true,
			Status:       STATUS_TEMPLATE,
			Summary:      query.Text,
			Tags:         query.Tags,
			Project:      query.Project,
			Priority:     query.Priority,
			Notes:        query.Note,
			Due:          query.Due,
		}
		task = ts.MustLoadTask(task)
		ts.SavePendingChanges()
		MustGitCommit(conf.Repo, "Created Template %s", task)
	}

	return nil
}

// CommandUndo performs undo with git revert.
func CommandUndo(conf Config, args []string, ctx, query Query) error {
	var err error

	n := 1
	if len(args) == 3 {
		n, err = strconv.Atoi(args[2])
		if err != nil {
			Help(CMD_UNDO)

			return err
		}
	}

	MustRunGitCmd(conf.Repo, "revert", "--no-gpg-sign", "--no-edit", "HEAD~"+strconv.Itoa(n)+"..")

	return nil
}

// CommandVersion prints version information for the dstask binary.
func CommandVersion() {
	fmt.Printf(
		"Version: %s\nGit commit: %s\nBuild date: %s\n",
		VERSION,
		GIT_COMMIT,
		BUILD_DATE,
	)
}

// resolveDependencyRefs maps block-on/unblock argument references (numeric ids
// beyond the first, plus UUID-shaped words) onto dependency task UUIDs.
func resolveDependencyRefs(ts *TaskSet, query Query) ([]string, error) {
	var uuids []string

	dependencyIDStart := 0
	if len(query.IDs) > 0 {
		dependencyIDStart = 1
	}
	for _, id := range query.IDs[dependencyIDStart:] {
		task, err := ts.GetByID(id)
		if err != nil {
			return nil, err
		}

		uuids = append(uuids, task.UUID)
	}

	for _, word := range strings.Fields(query.Text) {
		if id, err := strconv.Atoi(word); err == nil {
			task, err := ts.GetByID(id)
			if err != nil {
				return nil, err
			}
			uuids = append(uuids, task.UUID)
			continue
		}
		if !IsValidUUID4String(word) {
			return nil, fmt.Errorf("dependency reference is neither a task ID nor a UUID: %s", word)
		}

		if ts.tasksByUUID[word] == nil {
			return nil, fmt.Errorf("dependency UUID does not match any known task: %s", word)
		}

		uuids = append(uuids, word)
	}

	return uuids, nil
}

// dependencyTarget resolves the mutation subject without converting a UUID to
// the concurrency-unstable numeric ID. A UUID subject is the first text word;
// numeric subject IDs retain the original query representation.
func dependencyTarget(ts *TaskSet, query Query) (Task, Query, error) {
	if len(query.IDs) > 0 {
		return ts.MustGetByID(query.IDs[0]), query, nil
	}

	task, query, found, err := uuidMutationTarget(ts, query)
	if err != nil {
		return Task{}, query, err
	}
	if !found {
		return Task{}, query, errors.New("no target task ID or UUID specified")
	}
	return task, query, nil
}

// CommandBlockOn records dependencies on the target task: the first ID or UUID
// is the target, every further ID or UUID is a dependency it becomes blocked on.
func CommandBlockOn(conf Config, ctx, query Query) error {
	// resolved dependencies must be addressable, so load everything
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, true)
	if err != nil {
		return err
	}

	task, query, err := dependencyTarget(ts, query)
	if err != nil {
		return err
	}

	deps, err := resolveDependencyRefs(ts, query)
	if err != nil {
		return err
	}

	if len(deps) == 0 {
		return errors.New("no dependency references specified")
	}

	for _, dep := range deps {
		if dep == task.UUID {
			return errors.New("a task cannot block on itself")
		}

		if !StrSliceContains(task.Dependencies, dep) {
			task.Dependencies = append(task.Dependencies, dep)
		}
	}

	ts.MustUpdateTask(task)
	ts.SavePendingChanges()
	MustGitCommit(conf.Repo, "Blocked %s", task)

	return nil
}

// CommandUnblock removes the referenced dependencies from the target task, or
// all of them when no reference is given.
func CommandUnblock(conf Config, ctx, query Query) error {
	ts, err := LoadTaskSet(conf.Repo, conf.IDsFile, true)
	if err != nil {
		return err
	}

	task, query, err := dependencyTarget(ts, query)
	if err != nil {
		return err
	}

	deps, err := resolveDependencyRefs(ts, query)
	if err != nil {
		return err
	}

	if len(deps) == 0 {
		task.Dependencies = nil
	} else {
		var kept []string

		for _, existing := range task.Dependencies {
			if !StrSliceContains(deps, existing) {
				kept = append(kept, existing)
			}
		}

		task.Dependencies = kept
	}

	ts.MustUpdateTask(task)
	ts.SavePendingChanges()
	MustGitCommit(conf.Repo, "Unblocked %s", task)

	return nil
}
