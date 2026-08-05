package integration

import (
	"testing"

	"gotest.tools/assert"
)

func TestBlockOnExposesDependenciesAndBlocked(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "decide granularity")
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("add", "design layout")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks := unmarshalTaskArray(t, output)
	assert.Equal(t, 2, len(tasks))
	decideUUID := tasks[0].UUID

	output, exiterr, success = program("2", "block-on", "1")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks[1].Dependencies), "dependency should be recorded and visible in JSON")
	assert.Equal(t, decideUUID, tasks[1].Dependencies[0], "dependency is stored as the dep's UUID")
	assert.Equal(t, true, tasks[1].Blocked, "task with an unresolved dependency is blocked")
	assert.Equal(t, false, tasks[0].Blocked, "task without dependencies is not blocked")
}

func TestBlockedFilterAndResolutionUnblocks(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "decide granularity")
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("add", "design layout")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("2", "block-on", "1")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open", ":blocked")
	assertProgramResult(t, output, exiterr, success)
	tasks := unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks), ":blocked keeps only blocked tasks")
	assert.Equal(t, "design layout", tasks[0].Summary)

	output, exiterr, success = program("show-open", ":unblocked")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks), ":unblocked keeps only unblocked tasks")
	assert.Equal(t, "decide granularity", tasks[0].Summary)

	output, exiterr, success = program("1", "done")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open", ":blocked")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 0, len(tasks), "resolving the dependency unblocks the dependent")

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, false, tasks[0].Blocked, "blocked flag clears once the dependency is resolved")
}

func TestBlockOnByUUIDAndUnblock(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "decide granularity")
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("add", "design layout")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks := unmarshalTaskArray(t, output)
	decideUUID := tasks[0].UUID

	output, exiterr, success = program("2", "block-on", decideUUID)
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks[1].Dependencies), "block-on accepts a dependency by UUID")
	assert.Equal(t, decideUUID, tasks[1].Dependencies[0])

	// blocking twice on the same dependency does not duplicate it
	output, exiterr, success = program("2", "block-on", "1")
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks[1].Dependencies), "duplicate dependencies collapse")

	// a task cannot block on itself
	output, exiterr, success = program("2", "block-on", "2")
	assert.Equal(t, false, success, "self-dependency must be refused")

	output, exiterr, success = program("2", "unblock", decideUUID)
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("show-open")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, 0, len(tasks[1].Dependencies), "unblock removes the dependency")
	assert.Equal(t, false, tasks[1].Blocked)
}

func TestStartCapturesClaimIdentity(t *testing.T) {
	repo, cleanup := makeDstaskRepo(t)
	defer cleanup()

	program := testCmd(repo)

	output, exiterr, success := program("add", "decide granularity")
	assertProgramResult(t, output, exiterr, success)

	unset := setEnv("DSTASK_IDENTITY", "SilverGrove")
	defer unset()

	output, exiterr, success = program("1", "start")
	assertProgramResult(t, output, exiterr, success)

	output, exiterr, success = program("show-active")
	assertProgramResult(t, output, exiterr, success)
	tasks := unmarshalTaskArray(t, output)
	assert.Equal(t, 1, len(tasks))
	assert.Equal(t, "SilverGrove", tasks[0].DelegatedTo, "start records the claiming identity")

	// stopping keeps the last claimant for audit; status carries the claim
	output, exiterr, success = program("1", "stop")
	assertProgramResult(t, output, exiterr, success)
	output, exiterr, success = program("show-paused")
	assertProgramResult(t, output, exiterr, success)
	tasks = unmarshalTaskArray(t, output)
	assert.Equal(t, "SilverGrove", tasks[0].DelegatedTo)
}
