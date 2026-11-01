package deb

import (
	"strings"
	"testing"
	"time"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestWriteChangelog(t *testing.T) {
	date := func(year int, month time.Month, day int) dalec.Date {
		return dalec.Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
	}

	older := dalec.ChangelogEntry{
		Author:  "Old Author <old@example.com>",
		Date:    date(2024, time.January, 1),
		Changes: []string{"Initial release"},
	}
	newer := dalec.ChangelogEntry{
		Author:  "New Author <new@example.com>",
		Date:    date(2025, time.June, 15),
		Changes: []string{"Update upstream source", "Rebuild"},
	}

	const header = "test-pkg (1.2.3-ubuntu22.04u1) jammy; urgency=low\n\n"
	const newerChange = "  * Update upstream source\n" +
		"  * Rebuild\n" +
		" -- New Author <new@example.com>  Sun, 15 Jun 2025 00:00:00 +0000\n\n"

	write := func(t *testing.T, changelog []dalec.ChangelogEntry) string {
		t.Helper()
		spec := &dalec.Spec{
			Name:      "test-pkg",
			Version:   "1.2.3",
			Revision:  "1",
			Changelog: changelog,
		}

		var buf strings.Builder
		assert.NilError(t, WriteChangelog(&buf, spec, "jammy", "ubuntu22.04"))
		return buf.String()
	}

	t.Run("The newest entry is used when entries are listed newest first", func(t *testing.T) {
		got := write(t, []dalec.ChangelogEntry{newer, older})
		assert.Equal(t, got, header+newerChange)
	})

	t.Run("The newest entry is used when entries are listed oldest first", func(t *testing.T) {
		got := write(t, []dalec.ChangelogEntry{older, newer})
		assert.Equal(t, got, header+newerChange)
	})

	t.Run("The first listed entry is used when the newest date is shared", func(t *testing.T) {
		sameDay := dalec.ChangelogEntry{
			Author:  "Other Author <other@example.com>",
			Date:    newer.Date,
			Changes: []string{"Other change"},
		}

		got := write(t, []dalec.ChangelogEntry{older, newer, sameDay})
		assert.Equal(t, got, header+newerChange)
	})

	t.Run("A dummy entry is used when the spec has no changelog", func(t *testing.T) {
		got := write(t, nil)
		assert.Equal(t, got, header+
			"  * Dummy changelog entry\n"+
			" -- Dalec Dummy Changelog <"+placeholderMaintainerEmail+">  "+
			dummyChangelogEntry.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700")+"\n\n")
	})
}
