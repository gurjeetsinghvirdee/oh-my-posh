package segments

import (
	"fmt"
	"oh-my-posh/environment"
	"oh-my-posh/properties"
	"oh-my-posh/regex"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

// GitStatus represents part of the status of a git repository
type GitStatus struct {
	ScmStatus
}

func (s *GitStatus) add(code string) {
	switch code {
	case ".":
		return
	case "D":
		s.Deleted++
	case "A", "?":
		s.Added++
	case "U":
		s.Unmerged++
	case "M", "R", "C", "m":
		s.Modified++
	}
}

type Git struct {
	scm

	Working       *GitStatus
	Staging       *GitStatus
	Ahead         int
	Behind        int
	HEAD          string
	Ref           string
	Hash          string
	BranchStatus  string
	Upstream      string
	UpstreamIcon  string
	UpstreamURL   string
	StashCount    int
	WorktreeCount int
	IsWorkTree    bool

	gitWorkingFolder string // .git working folder
	gitRootFolder    string // .git root folder
	gitRealFolder    string // .git real folder(can be different from current path when in worktrees)

	gitCommand string

	IsWslSharedPath bool
}

const (
	// FetchStatus fetches the status of the repository
	FetchStatus properties.Property = "fetch_status"
	// FetchStashCount fetches the stash count
	FetchStashCount properties.Property = "fetch_stash_count"
	// FetchWorktreeCount fetches the worktree count
	FetchWorktreeCount properties.Property = "fetch_worktree_count"
	// FetchUpstreamIcon fetches the upstream icon
	FetchUpstreamIcon properties.Property = "fetch_upstream_icon"

	// BranchIcon the icon to use as branch indicator
	BranchIcon properties.Property = "branch_icon"
	// BranchIdenticalIcon the icon to display when the remote and local branch are identical
	BranchIdenticalIcon properties.Property = "branch_identical_icon"
	// BranchAheadIcon the icon to display when the local branch is ahead of the remote
	BranchAheadIcon properties.Property = "branch_ahead_icon"
	// BranchBehindIcon the icon to display when the local branch is behind the remote
	BranchBehindIcon properties.Property = "branch_behind_icon"
	// BranchGoneIcon the icon to use when ther's no remote
	BranchGoneIcon properties.Property = "branch_gone_icon"
	// RebaseIcon shows before the rebase context
	RebaseIcon properties.Property = "rebase_icon"
	// CherryPickIcon shows before the cherry-pick context
	CherryPickIcon properties.Property = "cherry_pick_icon"
	// RevertIcon shows before the revert context
	RevertIcon properties.Property = "revert_icon"
	// CommitIcon shows before the detached context
	CommitIcon properties.Property = "commit_icon"
	// NoCommitsIcon shows when there are no commits in the repo yet
	NoCommitsIcon properties.Property = "no_commits_icon"
	// TagIcon shows before the tag context
	TagIcon properties.Property = "tag_icon"
	// MergeIcon shows before the merge context
	MergeIcon properties.Property = "merge_icon"
	// GithubIcon shows√ when upstream is github
	GithubIcon properties.Property = "github_icon"
	// BitbucketIcon shows  when upstream is bitbucket
	BitbucketIcon properties.Property = "bitbucket_icon"
	// AzureDevOpsIcon shows  when upstream is azure devops
	AzureDevOpsIcon properties.Property = "azure_devops_icon"
	// GitlabIcon shows when upstream is gitlab
	GitlabIcon properties.Property = "gitlab_icon"
	// GitIcon shows when the upstream can't be identified
	GitIcon properties.Property = "git_icon"

	DETACHED     = "(detached)"
	BRANCHPREFIX = "ref: refs/heads/"
)

func (g *Git) Template() string {
	return " {{ .HEAD }} {{ .BranchStatus }}{{ if .Working.Changed }} \uF044 {{ .Working.String }}{{ end }}{{ if and (.Staging.Changed) (.Working.Changed) }} |{{ end }}{{ if .Staging.Changed }} \uF046 {{ .Staging.String }}{{ end }}{{ if gt .StashCount 0}} \uF692 {{ .StashCount }}{{ end }}{{ if gt .WorktreeCount 0}} \uf1bb {{ .WorktreeCount }}{{ end }} " // nolint: lll
}

func (g *Git) Enabled() bool {
	if !g.shouldDisplay() {
		return false
	}
	displayStatus := g.props.GetBool(FetchStatus, false)
	if displayStatus {
		g.setGitStatus()
		g.setGitHEADContext()
		g.setBranchStatus()
	} else {
		g.setPrettyHEADName()
		g.Working = &GitStatus{}
		g.Staging = &GitStatus{}
	}
	if g.Upstream != "" && g.props.GetBool(FetchUpstreamIcon, false) {
		g.UpstreamIcon = g.getUpstreamIcon()
	}
	if g.props.GetBool(FetchStashCount, false) {
		g.StashCount = g.getStashContext()
	}
	if g.props.GetBool(FetchWorktreeCount, false) {
		g.WorktreeCount = g.getWorktreeContext()
	}
	return true
}

func (g *Git) shouldDisplay() bool {
	// when in wsl/wsl2 and in a windows shared folder
	// we must use git.exe and convert paths accordingly
	// for worktrees, stashes, and path to work
	g.IsWslSharedPath = g.env.InWSLSharedDrive()
	if !g.env.HasCommand(g.getGitCommand()) {
		return false
	}
	gitdir, err := g.env.HasParentFilePath(".git")
	if err != nil {
		return false
	}
	if g.shouldIgnoreRootRepository(gitdir.ParentFolder) {
		return false
	}

	if gitdir.IsDir {
		g.gitWorkingFolder = gitdir.Path
		g.gitRootFolder = gitdir.Path
		// convert the worktree file path to a windows one when in wsl 2 shared folder
		g.gitRealFolder = strings.TrimSuffix(g.convertToWindowsPath(gitdir.Path), ".git")
		return true
	}
	// handle worktree
	g.gitRootFolder = gitdir.Path
	dirPointer := strings.Trim(g.env.FileContent(gitdir.Path), " \r\n")
	matches := regex.FindNamedRegexMatch(`^gitdir: (?P<dir>.*)$`, dirPointer)
	if matches != nil && matches["dir"] != "" {
		// if we open a worktree file in a shared wsl2 folder, we have to convert it back
		// to the mounted path
		g.gitWorkingFolder = g.convertToLinuxPath(matches["dir"])

		// in worktrees, the path looks like this: gitdir: path/.git/worktrees/branch
		// strips the last .git/worktrees part
		// :ind+5 = index + /.git
		ind := strings.LastIndex(g.gitWorkingFolder, "/.git/worktrees")
		if ind > -1 {
			g.gitRootFolder = g.gitWorkingFolder[:ind+5]
			g.gitRealFolder = strings.TrimSuffix(g.env.FileContent(g.gitWorkingFolder+"/gitdir"), ".git\n")
			g.IsWorkTree = true
			return true
		}
		// in submodules, the path looks like this: gitdir: ../.git/modules/test-submodule
		// we need the parent folder to detect where the real .git folder is
		ind = strings.LastIndex(g.gitWorkingFolder, "/.git/modules")
		if ind > -1 {
			g.gitRootFolder = gitdir.ParentFolder + "/" + g.gitWorkingFolder
			g.gitRealFolder = g.gitRootFolder
			g.gitWorkingFolder = g.gitRootFolder
			return true
		}

		return false
	}
	return false
}

func (g *Git) setBranchStatus() {
	getBranchStatus := func() string {
		if g.Ahead > 0 && g.Behind > 0 {
			return fmt.Sprintf(" %s%d %s%d", g.props.GetString(BranchAheadIcon, "\u2191"), g.Ahead, g.props.GetString(BranchBehindIcon, "\u2193"), g.Behind)
		}
		if g.Ahead > 0 {
			return fmt.Sprintf(" %s%d", g.props.GetString(BranchAheadIcon, "\u2191"), g.Ahead)
		}
		if g.Behind > 0 {
			return fmt.Sprintf(" %s%d", g.props.GetString(BranchBehindIcon, "\u2193"), g.Behind)
		}
		if g.Behind == 0 && g.Ahead == 0 && g.Upstream != "" {
			return fmt.Sprintf(" %s", g.props.GetString(BranchIdenticalIcon, "\u2261"))
		}
		if g.Upstream == "" {
			return fmt.Sprintf(" %s", g.props.GetString(BranchGoneIcon, "\u2262"))
		}
		return ""
	}
	g.BranchStatus = getBranchStatus()
}

func (g *Git) getUpstreamIcon() string {
	upstream := regex.ReplaceAllString("/.*", g.Upstream, "")
	g.UpstreamURL = g.getOriginURL(upstream)
	if strings.Contains(g.UpstreamURL, "github") {
		return g.props.GetString(GithubIcon, "\uF408 ")
	}
	if strings.Contains(g.UpstreamURL, "gitlab") {
		return g.props.GetString(GitlabIcon, "\uF296 ")
	}
	if strings.Contains(g.UpstreamURL, "bitbucket") {
		return g.props.GetString(BitbucketIcon, "\uF171 ")
	}
	if strings.Contains(g.UpstreamURL, "dev.azure.com") || strings.Contains(g.UpstreamURL, "visualstudio.com") {
		return g.props.GetString(AzureDevOpsIcon, "\uFD03 ")
	}
	return g.props.GetString(GitIcon, "\uE5FB ")
}

func (g *Git) setGitStatus() {
	addToStatus := func(status string) {
		const UNTRACKED = "?"
		if strings.HasPrefix(status, UNTRACKED) {
			g.Working.add(UNTRACKED)
			return
		}
		if len(status) <= 4 {
			return
		}
		workingCode := status[3:4]
		stagingCode := status[2:3]
		g.Working.add(workingCode)
		g.Staging.add(stagingCode)
	}
	const (
		HASH         = "# branch.oid "
		REF          = "# branch.head "
		UPSTREAM     = "# branch.upstream "
		BRANCHSTATUS = "# branch.ab "
	)
	g.Working = &GitStatus{}
	g.Staging = &GitStatus{}
	output := g.getGitCommandOutput("status", "-unormal", "--branch", "--porcelain=2")
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, HASH) && len(line) >= len(HASH)+7 {
			g.Hash = line[len(HASH) : len(HASH)+7]
			continue
		}
		if strings.HasPrefix(line, REF) && len(line) > len(REF) {
			g.Ref = line[len(REF):]
			continue
		}
		if strings.HasPrefix(line, UPSTREAM) && len(line) > len(UPSTREAM) {
			g.Upstream = line[len(UPSTREAM):]
			continue
		}
		if strings.HasPrefix(line, BRANCHSTATUS) && len(line) > len(BRANCHSTATUS) {
			status := line[len(BRANCHSTATUS):]
			splitted := strings.Split(status, " ")
			if len(splitted) >= 2 {
				g.Ahead, _ = strconv.Atoi(splitted[0])
				behind, _ := strconv.Atoi(splitted[1])
				g.Behind = -behind
			}
			continue
		}
		addToStatus(line)
	}
}

func (g *Git) getGitCommand() string {
	if len(g.gitCommand) > 0 {
		return g.gitCommand
	}
	g.gitCommand = "git"
	if g.env.GOOS() == environment.WindowsPlatform || g.IsWslSharedPath {
		g.gitCommand = "git.exe"
	}
	return g.gitCommand
}

func (g *Git) getGitCommandOutput(args ...string) string {
	args = append([]string{"-C", g.gitRealFolder, "--no-optional-locks", "-c", "core.quotepath=false", "-c", "color.status=false"}, args...)
	val, err := g.env.RunCommand(g.getGitCommand(), args...)
	if err != nil {
		return ""
	}
	return val
}

func (g *Git) setGitHEADContext() {
	branchIcon := g.props.GetString(BranchIcon, "\uE0A0")
	if g.Ref == DETACHED {
		g.setPrettyHEADName()
	} else {
		head := g.formatHEAD(g.Ref)
		g.HEAD = fmt.Sprintf("%s%s", branchIcon, head)
	}

	formatDetached := func() string {
		if g.Ref == DETACHED {
			return fmt.Sprintf("%sdetached at %s", branchIcon, g.HEAD)
		}
		return g.HEAD
	}

	getPrettyNameOrigin := func(file string) string {
		var origin string
		head := g.FileContents(g.gitWorkingFolder, file)
		if head == "detached HEAD" {
			origin = formatDetached()
		} else {
			head = strings.Replace(head, "refs/heads/", "", 1)
			origin = branchIcon + g.formatHEAD(head)
		}
		return origin
	}

	if g.env.HasFolder(g.gitWorkingFolder + "/rebase-merge") {
		origin := getPrettyNameOrigin("rebase-merge/head-name")
		onto := g.getGitRefFileSymbolicName("rebase-merge/onto")
		onto = g.formatHEAD(onto)
		step := g.FileContents(g.gitWorkingFolder, "rebase-merge/msgnum")
		total := g.FileContents(g.gitWorkingFolder, "rebase-merge/end")
		icon := g.props.GetString(RebaseIcon, "\uE728 ")
		g.HEAD = fmt.Sprintf("%s%s onto %s%s (%s/%s) at %s", icon, origin, branchIcon, onto, step, total, g.HEAD)
		return
	}
	if g.env.HasFolder(g.gitWorkingFolder + "/rebase-apply") {
		origin := getPrettyNameOrigin("rebase-apply/head-name")
		step := g.FileContents(g.gitWorkingFolder, "rebase-apply/next")
		total := g.FileContents(g.gitWorkingFolder, "rebase-apply/last")
		icon := g.props.GetString(RebaseIcon, "\uE728 ")
		g.HEAD = fmt.Sprintf("%s%s (%s/%s) at %s", icon, origin, step, total, g.HEAD)
		return
	}
	// merge
	commitIcon := g.props.GetString(CommitIcon, "\uF417")
	if g.hasGitFile("MERGE_MSG") {
		icon := g.props.GetString(MergeIcon, "\uE727 ")
		mergeContext := g.FileContents(g.gitWorkingFolder, "MERGE_MSG")
		matches := regex.FindNamedRegexMatch(`Merge (remote-tracking )?(?P<type>branch|commit|tag) '(?P<theirs>.*)'`, mergeContext)
		// head := g.getGitRefFileSymbolicName("ORIG_HEAD")
		if matches != nil && matches["theirs"] != "" {
			var headIcon, theirs string
			switch matches["type"] {
			case "tag":
				headIcon = g.props.GetString(TagIcon, "\uF412")
				theirs = matches["theirs"]
			case "commit":
				headIcon = commitIcon
				theirs = g.formatSHA(matches["theirs"])
			default:
				headIcon = branchIcon
				theirs = g.formatHEAD(matches["theirs"])
			}
			g.HEAD = fmt.Sprintf("%s%s%s into %s", icon, headIcon, theirs, formatDetached())
			return
		}
	}
	// sequencer status
	// see if a cherry-pick or revert is in progress, if the user has committed a
	// conflict resolution with 'git commit' in the middle of a sequence of picks or
	// reverts then CHERRY_PICK_HEAD/REVERT_HEAD will not exist so we have to read
	// the todo file.
	if g.hasGitFile("CHERRY_PICK_HEAD") {
		sha := g.FileContents(g.gitWorkingFolder, "CHERRY_PICK_HEAD")
		cherry := g.props.GetString(CherryPickIcon, "\uE29B ")
		g.HEAD = fmt.Sprintf("%s%s%s onto %s", cherry, commitIcon, g.formatSHA(sha), formatDetached())
		return
	}
	if g.hasGitFile("REVERT_HEAD") {
		sha := g.FileContents(g.gitWorkingFolder, "REVERT_HEAD")
		revert := g.props.GetString(RevertIcon, "\uF0E2 ")
		g.HEAD = fmt.Sprintf("%s%s%s onto %s", revert, commitIcon, g.formatSHA(sha), formatDetached())
		return
	}
	if g.hasGitFile("sequencer/todo") {
		todo := g.FileContents(g.gitWorkingFolder, "sequencer/todo")
		matches := regex.FindNamedRegexMatch(`^(?P<action>p|pick|revert)\s+(?P<sha>\S+)`, todo)
		if matches != nil && matches["sha"] != "" {
			action := matches["action"]
			sha := matches["sha"]
			switch action {
			case "p", "pick":
				cherry := g.props.GetString(CherryPickIcon, "\uE29B ")
				g.HEAD = fmt.Sprintf("%s%s%s onto %s", cherry, commitIcon, g.formatSHA(sha), formatDetached())
				return
			case "revert":
				revert := g.props.GetString(RevertIcon, "\uF0E2 ")
				g.HEAD = fmt.Sprintf("%s%s%s onto %s", revert, commitIcon, g.formatSHA(sha), formatDetached())
				return
			}
		}
	}
	g.HEAD = formatDetached()
}

func (g *Git) formatHEAD(head string) string {
	maxLength := g.props.GetInt(BranchMaxLength, 0)
	if maxLength == 0 || len(head) < maxLength {
		return head
	}
	symbol := g.props.GetString(TruncateSymbol, "")
	return head[0:maxLength] + symbol
}

func (g *Git) formatSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[0:7]
}

func (g *Git) hasGitFile(file string) bool {
	return g.env.HasFilesInDir(g.gitWorkingFolder, file)
}

func (g *Git) getGitRefFileSymbolicName(refFile string) string {
	ref := g.FileContents(g.gitWorkingFolder, refFile)
	return g.getGitCommandOutput("name-rev", "--name-only", "--exclude=tags/*", ref)
}

func (g *Git) setPrettyHEADName() {
	// we didn't fetch status, fallback to parsing the HEAD file
	if len(g.Hash) == 0 {
		HEADRef := g.FileContents(g.gitWorkingFolder, "HEAD")
		if strings.HasPrefix(HEADRef, BRANCHPREFIX) {
			branchName := strings.TrimPrefix(HEADRef, BRANCHPREFIX)
			g.HEAD = fmt.Sprintf("%s%s", g.props.GetString(BranchIcon, "\uE0A0"), g.formatHEAD(branchName))
			return
		}
		// no branch, points to commit
		if len(HEADRef) >= 7 {
			g.Hash = HEADRef[0:7]
		}
	}
	// check for tag
	tagName := g.getGitCommandOutput("describe", "--tags", "--exact-match")
	if len(tagName) > 0 {
		g.HEAD = fmt.Sprintf("%s%s", g.props.GetString(TagIcon, "\uF412"), tagName)
		return
	}
	// fallback to commit
	if len(g.Hash) == 0 {
		g.HEAD = g.props.GetString(NoCommitsIcon, "\uF594 ")
		return
	}
	g.HEAD = fmt.Sprintf("%s%s", g.props.GetString(CommitIcon, "\uF417"), g.Hash)
}

func (g *Git) getStashContext() int {
	stashContent := g.FileContents(g.gitRootFolder, "logs/refs/stash")
	if stashContent == "" {
		return 0
	}
	lines := strings.Split(stashContent, "\n")
	return len(lines)
}

func (g *Git) getWorktreeContext() int {
	if !g.env.HasFolder(g.gitRootFolder + "/worktrees") {
		return 0
	}
	worktreeFolders := g.env.FolderList(g.gitRootFolder + "/worktrees")
	return len(worktreeFolders)
}

func (g *Git) getOriginURL(upstream string) string {
	cleanSSHURL := func(url string) string {
		if strings.HasPrefix(url, "http") {
			return url
		}
		url = strings.TrimPrefix(url, "git://")
		url = strings.TrimPrefix(url, "git@")
		url = strings.TrimSuffix(url, ".git")
		url = strings.ReplaceAll(url, ":", "/")
		return fmt.Sprintf("https://%s", url)
	}
	var url string
	cfg, err := ini.Load(g.gitRootFolder + "/config")
	if err != nil {
		url = g.getGitCommandOutput("remote", "get-url", upstream)
		return cleanSSHURL(url)
	}
	url = cfg.Section("remote \"" + upstream + "\"").Key("url").String()
	if url == "" {
		url = g.getGitCommandOutput("remote", "get-url", upstream)
	}
	return cleanSSHURL(url)
}

func (g *Git) convertToWindowsPath(path string) string {
	if !g.IsWslSharedPath {
		return path
	}
	return g.env.ConvertToWindowsPath(path)
}

func (g *Git) convertToLinuxPath(path string) string {
	if !g.IsWslSharedPath {
		return path
	}
	return g.env.ConvertToLinuxPath(path)
}
