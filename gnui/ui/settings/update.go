package settings

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/ui/screen"
)

// statusText is the Updates row's description: where the check has got to.
func (s *Screen) statusText(e *screen.Env) string {
	sn := s.up.Snapshot()
	switch {
	case sn.Checking:
		return s.tx.checking
	case sn.Failed != "":
		if s.tx.failedFor != sn.Failed {
			s.tx.failedFor = sn.Failed
			s.tx.failed = i18n.Format(i18n.T("Could not check for updates: {error}"), "{error}", sn.Failed)
		}
		return s.tx.failed
	case sn.Release.Version != "":
		return s.tx.updateTo(sn.Release.Version)
	case sn.Checked:
		return s.tx.upToDate
	case sn.Last != "":
		return sn.Last
	case !s.up.HasFeed():
		return s.tx.noFeed
	}
	return s.tx.notChecked
}

// updateBlock is the update screen (UP-002, UP-003): the release, how its
// download is going, and what to do next.
func (s *Screen) updateBlock(e *screen.Env) {
	k := e.K.Kit
	c := e.C()
	sn := s.up.Snapshot()
	rexui.Label(k, s.tx.updateTitle, rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k, sn.Line, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	link := s.tx.releaseNotes
	action := ""
	switch sn.State {
	case Downloading:
		checking := sn.Total > 0 && sn.Received == sn.Total
		if checking {
			rexui.Label(k, s.tx.checkingDownload, rexui.LabelOpts{Style: rexui.TextSemibold})
		} else {
			rexui.Label(k, s.tx.downloading, rexui.LabelOpts{Style: rexui.TextSemibold})
		}
		if sn.Total > 0 {
			rexui.ProgressBar(k, s.tx.progress, float32(float64(sn.Received)/float64(sn.Total)), rexui.ProgressOpts{})
			f := c.Fmt().Float(float64(sn.Received)/1e6, 1).Str(" ").Str(s.tx.of).Str(" ").Float(float64(sn.Total)/1e6, 1).Str(" MB")
			bytesIn(e, 1, ui.Pct(1), f.Bytes(), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		} else {
			rexui.Spinner(k, s.tx.progress, rexui.SpinnerOpts{})
		}
	case DownloadReady:
		rexui.Label(k, s.tx.readyToInstall, rexui.LabelOpts{Style: rexui.TextSemibold})
		rexui.Label(k, s.tx.finishFirst, rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
		action = s.tx.restart
	case Installing:
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "installing", ui.Pct(1), ui.Children(1), 0)
		rexui.Spinner(k, s.tx.progress, rexui.SpinnerOpts{})
		rexui.Label(k, s.tx.preparing, rexui.LabelOpts{})
		k.EndContainer()
	default:
		if sn.State == DownloadFailed {
			rexui.Label(k, sn.Error, rexui.LabelOpts{Color: rexui.Of(theme.Danger), Wrap: true})
		}
		switch {
		case !sn.SupportKnown:
			rexui.Spinner(k, s.tx.progress, rexui.SpinnerOpts{})
		case sn.Support != "":
			rexui.Label(k, sn.Support, rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
			link = s.tx.fromGitHub
		case sn.State == DownloadFailed:
			action = s.tx.retry
		default:
			action = s.tx.download
		}
	}
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "update buttons", ui.Pct(1), ui.Children(1), 0)
	k.Spacer()
	if rexui.Link(k, link, rexui.LinkOpts{}).Clicked && sn.Release.URL != "" {
		s.openURL(releaseURL(sn.Release.URL)) // again here: whatever set it, only https leaves
	}
	if action != "" && rexui.Button(k, action, rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
		if sn.State == DownloadReady {
			s.up.Install()
		} else {
			s.up.Download()
		}
	}
	k.EndContainer()
}
