// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"

	"github.com/Unknwon/com"

	"github.com/go-gitea/gitea/modules/base"
	"github.com/go-gitea/gitea/modules/git"
	"github.com/go-gitea/gitea/modules/log"
	"github.com/go-gitea/gitea/modules/process"
)

// Diff line types.
const (
	DIFF_LINE_PLAIN = iota + 1
	DIFF_LINE_ADD
	DIFF_LINE_DEL
	DIFF_LINE_SECTION
)

const (
	DIFF_FILE_ADD = iota + 1
	DIFF_FILE_CHANGE
	DIFF_FILE_DEL
)

type DiffLine struct {
	LeftIdx  int
	RightIdx int
	Type     int
	Content  string
}

func (d DiffLine) GetType() int {
	return d.Type
}

type DiffSection struct {
	Name  string
	Lines []*DiffLine
}

type DiffFile struct {
	Name               string
	Index              int
	Addition, Deletion int
	Type               int
	IsCreated          bool
	IsDeleted          bool
	IsBin              bool
	Sections           []*DiffSection
}

type Diff struct {
	TotalAddition, TotalDeletion int
	Files                        []*DiffFile
}

func (diff *Diff) NumFiles() int {
	return len(diff.Files)
}

const DIFF_HEAD = "diff --git "

func ParsePatch(pid int64, maxlines int, cmd *exec.Cmd, reader io.Reader) (*Diff, error) {
	scanner := bufio.NewScanner(reader)
	var (
		curFile    *DiffFile
		curSection = &DiffSection{
			Lines: make([]*DiffLine, 0, 10),
		}

		leftLine, rightLine int
		isTooLong           bool
		// FIXME: use first 30 lines to detect file encoding. Should use cache in the future.
		buf bytes.Buffer
	)

	diff := &Diff{Files: make([]*DiffFile, 0)}
	var i int
	for scanner.Scan() {
		line := scanner.Text()
		//fmt.Println(i, line)
		if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			continue
		}

		if line == "" {
			continue
		}

		i = i + 1

		// FIXME: use first 30 lines to detect file encoding.
		if i <= 30 {
			buf.WriteString(line)
		}

		// Diff data too large, we only show the first about maxlines lines
		if i == maxlines {
			isTooLong = true
			log.Warn("Diff data too large")
			//return &Diff{}, nil
		}

		switch {
		case line[0] == ' ':
			diffLine := &DiffLine{Type: DIFF_LINE_PLAIN, Content: line, LeftIdx: leftLine, RightIdx: rightLine}
			leftLine++
			rightLine++
			curSection.Lines = append(curSection.Lines, diffLine)
			continue
		case line[0] == '@':
			if isTooLong {
				return diff, nil
			}

			curSection = &DiffSection{}
			curFile.Sections = append(curFile.Sections, curSection)
			ss := strings.Split(line, "@@")
			diffLine := &DiffLine{Type: DIFF_LINE_SECTION, Content: line}
			curSection.Lines = append(curSection.Lines, diffLine)

			// Parse line number.
			ranges := strings.Split(ss[len(ss)-2][1:], " ")
			leftLine, _ = com.StrTo(strings.Split(ranges[0], ",")[0][1:]).Int()
			rightLine, _ = com.StrTo(strings.Split(ranges[1], ",")[0]).Int()
			continue
		case line[0] == '+':
			curFile.Addition++
			diff.TotalAddition++
			diffLine := &DiffLine{Type: DIFF_LINE_ADD, Content: line, RightIdx: rightLine}
			rightLine++
			curSection.Lines = append(curSection.Lines, diffLine)
			continue
		case line[0] == '-':
			curFile.Deletion++
			diff.TotalDeletion++
			diffLine := &DiffLine{Type: DIFF_LINE_DEL, Content: line, LeftIdx: leftLine}
			if leftLine > 0 {
				leftLine++
			}
			curSection.Lines = append(curSection.Lines, diffLine)
		case strings.HasPrefix(line, "Binary"):
			curFile.IsBin = true
			continue
		}

		// Get new file.
		if strings.HasPrefix(line, DIFF_HEAD) {
			if isTooLong {
				return diff, nil
			}

			fs := strings.Split(line[len(DIFF_HEAD):], " ")
			a := fs[0]

			curFile = &DiffFile{
				Name:     a[strings.Index(a, "/")+1:],
				Index:    len(diff.Files) + 1,
				Type:     DIFF_FILE_CHANGE,
				Sections: make([]*DiffSection, 0, 10),
			}
			diff.Files = append(diff.Files, curFile)

			// Check file diff type.
			for scanner.Scan() {
				switch {
				case strings.HasPrefix(scanner.Text(), "new file"):
					curFile.Type = DIFF_FILE_ADD
					curFile.IsDeleted = false
					curFile.IsCreated = true
				case strings.HasPrefix(scanner.Text(), "deleted"):
					curFile.Type = DIFF_FILE_DEL
					curFile.IsCreated = false
					curFile.IsDeleted = true
				case strings.HasPrefix(scanner.Text(), "index"):
					curFile.Type = DIFF_FILE_CHANGE
					curFile.IsCreated = false
					curFile.IsDeleted = false
				}
				if curFile.Type > 0 {
					break
				}
			}
		}
	}

	// FIXME: use first 30 lines to detect file encoding.
	charsetLabel, err := base.DetectEncoding(buf.Bytes())
	if charsetLabel != "utf8" && err == nil {
		encoding, _ := charset.Lookup(charsetLabel)

		if encoding != nil {
			d := encoding.NewDecoder()
			for _, f := range diff.Files {
				for _, sec := range f.Sections {
					for _, l := range sec.Lines {
						if c, _, err := transform.String(d, l.Content); err == nil {
							l.Content = c
						}
					}
				}
			}
		}
	}

	return diff, nil
}

func GetDiffRange(repoPath, beforeCommitId string, afterCommitId string, maxlines int) (*Diff, error) {
	repo, err := git.OpenRepository(repoPath)
	if err != nil {
		return nil, err
	}

	commit, err := repo.GetCommit(afterCommitId)
	if err != nil {
		return nil, err
	}

	rd, wr := io.Pipe()
	var cmd *exec.Cmd
	// if "after" commit given
	if beforeCommitId == "" {
		// First commit of repository.
		if commit.ParentCount() == 0 {
			cmd = exec.Command("git", "show", afterCommitId)
		} else {
			c, _ := commit.Parent(0)
			cmd = exec.Command("git", "diff", c.Id.String(), afterCommitId)
		}
	} else {
		cmd = exec.Command("git", "diff", beforeCommitId, afterCommitId)
	}
	cmd.Dir = repoPath
	cmd.Stdout = wr
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	done := make(chan error)
	go func() {
		cmd.Start()
		done <- cmd.Wait()
		wr.Close()
	}()
	defer rd.Close()

	desc := fmt.Sprintf("GetDiffRange(%s)", repoPath)
	pid := process.Add(desc, cmd)
	go func() {
		// In case process became zombie.
		select {
		case <-time.After(5 * time.Minute):
			if errKill := process.Kill(pid); errKill != nil {
				log.Error(4, "git_diff.ParsePatch(Kill): %v", err)
			}
			<-done
			// return "", ErrExecTimeout.Error(), ErrExecTimeout
		case err = <-done:
			process.Remove(pid)
		}
	}()

	return ParsePatch(pid, maxlines, cmd, rd)
}

func GetDiffForkedRange(repoPath, forkedRepoPath, beforeBranch string, afterBranch string, maxlines int) (*Diff, error) {
	if !git.IsRemoteExist(forkedRepoPath, "upstream") {
		_, _, err := com.ExecCmdDir(forkedRepoPath, "git", "remote", "add", "upstream", repoPath)
		if err != nil {
			return nil, err
		}
	}

	if !git.IsRemoteBranchExist(forkedRepoPath, "upstream", beforeBranch) {
		_, _, err := com.ExecCmdDir(forkedRepoPath, "git", "remote", "update")
		if err != nil {
			return nil, err
		}
	}

	beforeBranch = "remotes/upstream/"+beforeBranch

	rd, wr := io.Pipe()
	var cmd *exec.Cmd
	// if "after" commit given

	cmd = exec.Command("git", "diff", beforeBranch, afterBranch)
	cmd.Dir = forkedRepoPath
	cmd.Stdout = wr
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	done := make(chan error)
	go func() {
		cmd.Start()
		done <- cmd.Wait()
		wr.Close()
	}()
	defer rd.Close()

	var err error

	desc := fmt.Sprintf("GetDiffRange(%s)", repoPath)
	pid := process.Add(desc, cmd)
	go func() {
		// In case process became zombie.
		select {
		case <-time.After(5 * time.Minute):
			if errKill := process.Kill(pid); errKill != nil {
				log.Error(4, "git_diff.ParsePatch(Kill): %v", err)
			}
			<-done
			// return "", ErrExecTimeout.Error(), ErrExecTimeout
		case err = <-done:
			process.Remove(pid)
		}
	}()

	return ParsePatch(pid, maxlines, cmd, rd)
}

func GetDiffCommit(repoPath, commitId string, maxlines int) (*Diff, error) {
	return GetDiffRange(repoPath, "", commitId, maxlines)
}
