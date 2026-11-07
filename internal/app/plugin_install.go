package app

import (
	"errors"
	"log"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/plugin"
)

// Plugins install from GitHub repositories, and update from where they came, or
// from a folder or zip on this machine. A link on the web installs one too:
//
//	mygo-music://install-plugin?repo=owner/repo
//
// which opens the plugins' settings and asks about it there, as one chosen from
// the + menu of their header is asked about. Nothing installs without the
// listener allowing it.

// linkScheme is the scheme of the app's links, declared in mygo.config.ts: the
// development app's is its own, which leaves the installed app's links to it.
var linkScheme = func() string {
	if mygo.IsDev() {
		return "mygo-music-dev"
	}
	return "mygo-music"
}()

// These reach GitHub, and the disk; tests replace them.
var (
	fetchPlugin  = plugin.Fetch
	fetchRelease = plugin.FetchRelease
	latestPlugin = plugin.Latest
	openPlugin   = plugin.Open
)

// pickPlugin asks the listener for a plugin's folder, or a zip of one, with a
// native dialog, on a goroutine, for it blocks; tests replace it.
var pickPlugin = func(a *app, folder bool, got func(string)) {
	go func() {
		opts := mygo.OpenDialogOptions{Title: T("Install a Plugin"), ButtonLabel: T("Install"), Directory: folder}
		if !folder {
			opts.Filters = []mygo.FileFilter{{Name: T("Zip Archive"), Extensions: []string{"zip"}}}
		}
		paths, err := mygo.Dialog.Open(opts)
		if err == nil && len(paths) > 0 {
			a.post(func() { got(paths[0]) })
		}
	}()
}

// confirmRemovePlugin asks the listener with a native dialog whether to remove a
// plugin, on a goroutine, for it blocks, calling yes if they say so; tests
// replace it, and trashItem.
var confirmRemovePlugin = func(a *app, name string, yes func()) {
	parent := a.settingsWin
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:             parent,
			Type:               mygo.MessageWarning,
			Message:            Tf("Remove %s?", name),
			Detail:             T("Its folder moves to the Trash. Its settings stay, in case you install it again."),
			Buttons:            []string{T("Cancel"), T("Remove")},
			DefaultButton:      1,
			DestructiveButtons: []int{1},
		})
		if err == nil && res.Button == 1 {
			a.post(yes)
		}
	}()
}

var trashItem = mygo.Shell.TrashItem

// pluginCheckEvery is how often plugins are checked for new versions: once a day,
// as the app is. GitHub's API allows few requests without an account.
const pluginCheckEvery = 24 * time.Hour

// pluginInstaller is what's going on with installing plugins.
type pluginInstaller struct {
	// open is whether the dialog shows: asking for a repository, with github,
	// or about a plugin on its way, or what went wrong with one.
	open, github bool
	repo         string          // what's written in the field
	busy         bool            // one's downloading
	err          string          // what went wrong last
	pending      *plugin.Pending // one downloaded, for the listener to allow
	updating     map[string]bool
	updateErr    map[string]string // what went wrong updating a plugin, by ID
	// checked is what checking a plugin for a new version came to, when the
	// listener asked, by ID.
	checked map[string]checkResult
	// found is the new version checking found of a plugin, by ID, for updating
	// to install without asking GitHub again; after the app opens again, it asks.
	found map[string]*plugin.Release
}

// checkResult is what checking a plugin for a new version came to.
type checkResult int

const (
	checking checkResult = iota + 1
	upToDate
	checkFailed
)

// askForRepo opens the dialog that asks for a repository to install from.
func (a *app) askForRepo() {
	in := &a.installer
	a.cancelInstall()
	in.open, in.github, in.err = true, true, ""
}

// installFromRepo downloads what's written in the field, to ask about it.
func (a *app) installFromRepo() {
	in := &a.installer
	repo, err := plugin.ParseRepo(in.repo)
	if err != nil {
		in.err = T("Write a repository as owner/repo, or its address on GitHub")
		return
	}
	in.busy, in.err = true, ""
	a.cancelInstall()
	go func() {
		pending, err := fetchPlugin(repo, pluginsDir())
		a.post(func() {
			in.busy = false
			if err != nil {
				in.err = installError(err)
				return
			}
			in.pending = pending
		})
	}()
}

// openLink acts on a link of the app's scheme. One to install a plugin shows the
// plugins' settings with its repository in the field, and downloads it to ask
// about it, unless another is downloading, when Install takes it after.
func (a *app) openLink(raw string) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, linkScheme) {
		return
	}
	// mygo-music://install-plugin, or mygo-music:install-plugin.
	action := u.Host
	if action == "" {
		action = u.Opaque + u.Path
	}
	if strings.Trim(action, "/") != "install-plugin" {
		log.Printf("unknown link: %s", raw)
		return
	}
	repo := strings.TrimSpace(u.Query().Get("repo"))
	if repo == "" {
		return
	}
	in := &a.installer
	in.repo = repo
	in.open, in.github = true, true
	a.openSourceSettings()
	if a.win != nil {
		// The link was opened in the browser, which is in front.
		mygo.App.Focus()
	}
	if !in.busy {
		a.installFromRepo()
	}
	a.redraw()
}

// installFromDisk asks for a plugin's folder, or a zip of one, and copies it in
// to ask about it, as one from GitHub is asked about.
func (a *app) installFromDisk(folder bool) {
	in := &a.installer
	pickPlugin(a, folder, func(path string) {
		a.cancelInstall()
		in.open, in.github, in.busy, in.err = true, false, true, ""
		go func() {
			pending, err := openPlugin(path, pluginsDir())
			a.post(func() {
				in.busy = false
				if err != nil {
					in.err = installError(err)
					return
				}
				in.pending = pending
			})
		}()
	})
}

// installError says what went wrong, in the listener's language if it can.
func installError(err error) string {
	if errors.Is(err, plugin.ErrNoRepo) {
		return T("There's no such public repository on GitHub")
	}
	if needs, ok := errors.AsType[*plugin.NeedsNewerApp](err); ok {
		return Tf("%s needs MyGo Music %s or later. Update MyGo Music first.", needs.Plugin, needs.Version)
	}
	return err.Error()
}

// confirmInstall installs the plugin the listener allowed.
func (a *app) confirmInstall() {
	in := &a.installer
	pending := in.pending
	if pending == nil {
		return
	}
	in.pending, in.busy = nil, true
	go func() {
		err := pending.Install(pluginsDir())
		a.post(func() {
			in.busy = false
			if err != nil {
				in.err = installError(err)
				return
			}
			in.repo = ""
			if err := a.pluginInstalled(pending.Manifest.ID); err != nil {
				in.err = err.Error()
				return
			}
			in.open = false
		})
	}()
}

// cancelInstall forgets the plugin the listener didn't want.
func (a *app) cancelInstall() {
	in := &a.installer
	if in.pending != nil {
		go in.pending.Discard()
		in.pending = nil
	}
}

// pluginOf is a library's plugin, if it's one that's installed.
func pluginOf(lib *library) *plugin.Plugin {
	p, _ := lib.src.(*plugin.Plugin)
	return p
}

// pluginInstalled takes in what was just installed: a new plugin, or a new
// version of one, which takes its place without opening the app again.
func (a *app) pluginInstalled(id string) error {
	in := &a.installer
	delete(a.settings.PluginUpdates, id)
	a.settings.save()
	delete(in.updating, id)
	delete(in.found, id)
	p, err := plugin.Load(filepath.Join(pluginsDir(), id), userDataDir("plugin-data"))
	if err != nil {
		return err
	}
	lib := newLibrary(p)
	at := slices.IndexFunc(a.allLibs, func(l *library) bool { return l.id == id })
	if at < 0 {
		a.allLibs = append(a.allLibs, lib)
		a.plugins = append(a.plugins, p)
		a.enabledChanged()
		a.redraw()
		return nil
	}
	old := a.allLibs[at]
	a.allLibs[at] = lib
	if was := pluginOf(old); was != nil {
		a.plugins = slices.DeleteFunc(a.plugins, func(q *plugin.Plugin) bool { return q == was })
		// Its code is gone from under it: it stops, and the new one starts.
		go was.Close()
	}
	a.plugins = append(a.plugins, p)
	a.enabledChanged()
	if a.library == old {
		a.library = lib
		a.startLibrary(lib)
		a.load(a.route)
	}
	a.redraw()
	return nil
}

// removePlugin takes a plugin out, what it keeps of its own staying for if it
// comes back. The last one on stays, as there would be nothing to show.
func (a *app) removePlugin(lib *library) {
	p := pluginOf(lib)
	if p == nil || (len(a.libs) == 1 && a.libs[0] == lib) {
		return
	}
	a.allLibs = slices.DeleteFunc(slices.Clone(a.allLibs), func(l *library) bool { return l == lib })
	a.plugins = slices.DeleteFunc(a.plugins, func(q *plugin.Plugin) bool { return q == p })
	a.enabledChanged()
	if a.library == lib {
		a.switchSource(a.libs[0].id)
	}
	delete(a.settings.PluginUpdates, lib.id)
	delete(a.installer.found, lib.id)
	a.settings.save()
	go func() {
		if err := p.Remove(trashItem); err != nil {
			log.Printf("removing plugin %s: %v", lib.id, err)
		}
	}()
	a.redraw()
}

// keepCheckingPlugins checks for new versions now if it's time, and from then on
// as the day comes round, for as long as the app runs.
func (a *app) keepCheckingPlugins() {
	a.checkPluginUpdates()
	time.AfterFunc(pluginCheckEvery, func() { a.post(a.keepCheckingPlugins) })
}

// checkPluginUpdates asks where the plugins came from what versions are there now,
// unless it's been done in the last day, what it found then being kept. What can't
// be reached just shows nothing new.
func (a *app) checkPluginUpdates() {
	if !a.settings.PluginsChecked.IsZero() && time.Since(a.settings.PluginsChecked) < pluginCheckEvery {
		return
	}
	origins := map[string]plugin.Origin{}
	for _, lib := range a.allLibs {
		if p := pluginOf(lib); p != nil {
			if o := p.Origin(); o != nil {
				origins[lib.id] = *o
			}
		}
	}
	if len(origins) == 0 {
		return
	}
	a.settings.PluginsChecked = time.Now()
	a.settings.save()
	go func() {
		found, releases := map[string]string{}, map[string]*plugin.Release{}
		for id, o := range origins {
			rel, err := latestPlugin(o.Repo)
			if err != nil {
				log.Printf("checking plugin %s: %v", id, err)
				continue
			}
			if plugin.IsNewer(rel.Version, o.Version) {
				found[id], releases[id] = rel.Version, rel
			}
		}
		a.post(func() {
			a.settings.PluginUpdates = found
			a.installer.found = releases
			a.settings.save()
			a.redraw()
		})
	}()
}

// checkPluginUpdate checks one plugin for a new version now, as the listener
// asks from its menu, whenever the daily check was. A new one shows its Update
// button; none, or none to be had, is said in its row.
func (a *app) checkPluginUpdate(lib *library) {
	p := pluginOf(lib)
	if p == nil || p.Origin() == nil {
		return
	}
	in := &a.installer
	if in.checked == nil {
		in.checked = map[string]checkResult{}
	}
	id, o := lib.id, *p.Origin()
	if in.checked[id] == checking {
		return
	}
	in.checked[id] = checking
	delete(in.updateErr, id)
	go func() {
		rel, err := latestPlugin(o.Repo)
		a.post(func() {
			switch {
			case err != nil:
				log.Printf("checking plugin %s: %v", id, err)
				in.checked[id] = checkFailed
			case plugin.IsNewer(rel.Version, o.Version):
				delete(in.checked, id)
				if a.settings.PluginUpdates == nil {
					a.settings.PluginUpdates = map[string]string{}
				}
				a.settings.PluginUpdates[id] = rel.Version
				if in.found == nil {
					in.found = map[string]*plugin.Release{}
				}
				in.found[id] = rel
				a.settings.save()
			default:
				in.checked[id] = upToDate
				delete(a.settings.PluginUpdates, id)
				delete(in.found, id)
				a.settings.save()
			}
			a.redraw()
		})
	}()
}

// updatePlugin installs the new version of a plugin. One that asks for more than
// the listener allowed asks them again, first.
func (a *app) updatePlugin(lib *library) {
	p := pluginOf(lib)
	if p == nil {
		return
	}
	o := p.Origin()
	if o == nil {
		return
	}
	in := &a.installer
	if in.updating == nil {
		in.updating = map[string]bool{}
	}
	if in.updateErr == nil {
		in.updateErr = map[string]string{}
	}
	in.updating[lib.id] = true
	delete(in.updateErr, lib.id)
	id := lib.id
	had := p.Permissions()
	// The version the check found, which the Update button names, rather than
	// asking GitHub again for what's there.
	rel := in.found[id]
	if rel != nil && rel.Version != a.settings.PluginUpdates[id] {
		rel = nil
	}
	go func() {
		var pending *plugin.Pending
		var err error
		if rel != nil {
			pending, err = fetchRelease(rel, pluginsDir())
		} else {
			pending, err = fetchPlugin(o.Repo, pluginsDir())
		}
		if err == nil && asksForMore(had, pending.Manifest.Permissions) {
			a.post(func() {
				delete(in.updating, id)
				a.cancelInstall()
				in.open, in.github, in.err, in.pending = true, false, "", pending
			})
			return
		}
		if err == nil {
			err = pending.Install(pluginsDir())
		}
		a.post(func() {
			delete(in.updating, id)
			if err == nil {
				err = a.pluginInstalled(id)
			}
			if err != nil {
				in.updateErr[id] = installError(err)
			}
		})
	}()
}

// asksForMore reports whether a new version asks for anything the old didn't.
func asksForMore(had, wants plugin.Permissions) bool {
	return more(had.Process, wants.Process) || more(had.Network, wants.Network)
}

// more reports whether wants has something had doesn't, which * has all of.
func more(had, wants []string) bool {
	if slices.Contains(had, "*") {
		return false
	}
	for _, w := range wants {
		if !slices.Contains(had, w) {
			return true
		}
	}
	return false
}

// installMenu is the + of the plugins' header: the ways to install one.
func (a *app) installMenu(c *ui.Context, p palette) {
	in := &a.installer
	add := iconButton(c.Key("install-plugin"), iconPlus, 16, p.muted, T("Install a Plugin"))
	add.Menu(func(m *ui.Menu) {
		if m.Item(T("From GitHub…")).Disabled(in.busy).Chosen() {
			a.askForRepo()
		}
		if m.Item(T("From a Folder…")).Disabled(in.busy).Chosen() {
			a.installFromDisk(true)
		}
		if m.Item(T("From a Zip…")).Disabled(in.busy).Chosen() {
			a.installFromDisk(false)
		}
		m.Separator()
		if m.Item(T("Show Plugins Folder")).Chosen() {
			revealFolder(pluginsDir())
		}
	})
}

// installDialog asks for a repository to install from, and what's asked about a
// plugin on its way, before it's in. Put away, it forgets the plugin.
func (a *app) installDialog(c *ui.Context, p palette) {
	in := &a.installer
	if !in.open && in.pending != nil {
		a.cancelInstall()
	}
	if !in.open && !in.busy {
		in.err = ""
	}
	ui.DialogBase(c.Key("install-plugin-dialog"), &in.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(380).Padding(24).Radius(18).Gap(10).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.4)).
			Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.94}, Exit: &ui.Motion{Scale: 0.96}})
		buttons := func(action string, disabled bool, label string, do func()) {
			ui.Row(c).Gap(10).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
				if pillButton(c.Key("cancel"), nil, T("Cancel"), p.hover.Over(p.bg), p.text).Clicked() {
					in.open = false
				}
				if action == "" {
					return
				}
				ok := pillButton(c.Key("ok"), nil, action, p.accent, p.onAccent).Label(label)
				if disabled {
					ok.Disabled(true).Opacity(0.5)
				}
				if ok.Clicked() && !disabled {
					do()
				}
			})
		}
		problem := func() {
			if in.err != "" {
				ui.Text(c, in.err).FontSize(12.5).TextColor(p.accent).LineHeight(1.4)
			}
		}
		switch pending := in.pending; {
		case pending != nil:
			ui.Text(c, Tf("Install %s?", strings.TrimSpace(pending.Manifest.Name+" "+pending.Version))).FontSize(17).FontWeight(700)
			from := pending.Repo
			if from == "" {
				from = pending.Path
			}
			ui.Text(c, Tf("From %s", from)).FontSize(13).TextColor(p.muted).LineHeight(1.45)
			perms := pending.Manifest.Permissions
			switch {
			case slices.Contains(perms.Network, "*"):
				ui.Text(c, T("Can reach any website")).FontSize(13).TextColor(p.muted)
			case len(perms.Network) > 0:
				ui.Text(c, Tf("Can reach %s", strings.Join(perms.Network, ", "))).FontSize(13).TextColor(p.muted).LineHeight(1.45)
			default:
				ui.Text(c, T("Asks for no network")).FontSize(13).TextColor(p.muted)
			}
			if len(perms.Process) > 0 {
				ui.Text(c, programsWarning(perms.Process)).FontSize(13).TextColor(p.muted).LineHeight(1.45)
			}
			problem()
			buttons(T("Install"), in.busy, Tf("Install %s", pending.Manifest.Name), a.confirmInstall)
		case in.github:
			ui.Text(c, T("Install from GitHub")).FontSize(17).FontWeight(700)
			ui.Text(c, T("A repository with a plugin, as owner/repo or its address on GitHub")).FontSize(13).TextColor(p.muted).LineHeight(1.45)
			field := ui.TextInput(c.Key("repo"), &in.repo).Label(T("Install from GitHub")).Placeholder(T("owner/repo or URL")).FontSize(14).AutoFocus()
			problem()
			action := T("Install")
			if in.busy {
				action = T("Downloading…")
			}
			empty := strings.TrimSpace(in.repo) == ""
			if field.Submitted() && !in.busy && !empty {
				a.installFromRepo()
			}
			buttons(action, in.busy || empty, action, a.installFromRepo)
		default:
			// One from this computer on its way in, or what went wrong with it.
			ui.Text(c, T("Install a Plugin")).FontSize(17).FontWeight(700)
			if in.busy {
				ui.Text(c, T("Opening the plugin…")).FontSize(13).TextColor(p.muted)
			}
			problem()
			buttons("", false, "", nil)
		}
	})
}

// pluginFrom says where a plugin came from: its repository, with its address
// on GitHub, or this computer, as one installed from a folder or dropped in,
// with none.
func pluginFrom(lib *library) (from, url string) {
	plug := pluginOf(lib)
	if plug == nil {
		return "", ""
	}
	if o := plug.Origin(); o != nil {
		return o.Repo, "https://github.com/" + o.Repo
	}
	return T("From this computer"), ""
}

// pluginUpdateButton installs the new version of a plugin from GitHub, when
// there's one.
func (a *app) pluginUpdateButton(c *ui.Context, lib *library) {
	plug := pluginOf(lib)
	if plug == nil {
		return
	}
	o := plug.Origin()
	version, ok := a.settings.PluginUpdates[lib.id]
	if o == nil || !ok || !plugin.IsNewer(version, o.Version) {
		return
	}
	in := &a.installer
	label := Tf("Update to %s", version)
	if in.updating[lib.id] {
		label = T("Updating…")
	}
	if ui.Button(c.Key("update"), label).Disabled(in.updating[lib.id]).Clicked() {
		a.updatePlugin(lib)
	}
}

// pluginUpdateError says what went wrong updating a plugin, if anything did,
// or what checking it for a new version came to.
func (a *app) pluginUpdateError(c *ui.Context, lib *library, p palette) {
	if msg := a.installer.updateErr[lib.id]; msg != "" {
		ui.Text(c, msg).FontSize(12).TextColor(p.accent).LineHeight(1.4)
		return
	}
	switch a.installer.checked[lib.id] {
	case checking:
		ui.Text(c, T("Checking for updates…")).FontSize(12).TextColor(p.muted)
	case upToDate:
		ui.Text(c, T("Up to date")).FontSize(12).TextColor(p.muted)
	case checkFailed:
		ui.Text(c, T("Couldn't check for updates")).FontSize(12).TextColor(p.accent)
	}
}

// pluginMenu is what else can be done with a plugin: showing its folder, and
// removing it, but the last one on, as there would be nothing to show.
func (a *app) pluginMenu(c *ui.Context, lib *library, p palette) {
	plug := pluginOf(lib)
	if plug == nil {
		return
	}
	more := iconButton(c.Key("plugin-more"), iconMore, 16, p.muted, Tf("More for %s", lib.name()))
	more.Menu(func(m *ui.Menu) {
		if plug.Origin() != nil {
			busy := a.installer.checked[lib.id] == checking || a.installer.updating[lib.id]
			if m.Item(T("Check for Updates")).Disabled(busy).Chosen() {
				a.checkPluginUpdate(lib)
			}
		}
		if m.Item(T("Show Folder")).Chosen() {
			revealFolder(plug.Dir())
		}
		m.Separator()
		last := len(a.libs) == 1 && a.libs[0] == lib
		if m.Item(T("Remove…")).Disabled(last || a.installer.updating[lib.id]).Chosen() {
			confirmRemovePlugin(a, lib.name(), func() { a.removePlugin(lib) })
		}
	})
}
