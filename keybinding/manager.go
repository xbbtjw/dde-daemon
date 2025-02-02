/*
 * Copyright (C) 2016 ~ 2018 Deepin Technology Co., Ltd.
 *
 * Author:     jouyouyun <jouyouwen717@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package keybinding

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	dbus "github.com/godbus/dbus"
	"github.com/linuxdeepin/dde-daemon/keybinding/shortcuts"
	airplanemode "github.com/linuxdeepin/go-dbus-factory/com.deepin.daemon.airplanemode"
	backlight "github.com/linuxdeepin/go-dbus-factory/com.deepin.daemon.helper.backlight"
	inputdevices "github.com/linuxdeepin/go-dbus-factory/com.deepin.daemon.inputdevices"
	keyevent "github.com/linuxdeepin/go-dbus-factory/com.deepin.daemon.keyevent"
	kwayland "github.com/linuxdeepin/go-dbus-factory/com.deepin.daemon.kwayland"
	lockfront "github.com/linuxdeepin/go-dbus-factory/com.deepin.dde.lockfront"
	shutdownfront "github.com/linuxdeepin/go-dbus-factory/com.deepin.dde.shutdownfront"
	sessionmanager "github.com/linuxdeepin/go-dbus-factory/com.deepin.sessionmanager"
	power "github.com/linuxdeepin/go-dbus-factory/com.deepin.system.power"
	wm "github.com/linuxdeepin/go-dbus-factory/com.deepin.wm"
	login1 "github.com/linuxdeepin/go-dbus-factory/org.freedesktop.login1"
	gio "github.com/linuxdeepin/go-gir/gio-2.0"
	"github.com/linuxdeepin/go-lib/dbusutil"
	"github.com/linuxdeepin/go-lib/dbusutil/gsprop"
	"github.com/linuxdeepin/go-lib/dbusutil/proxy"
	"github.com/linuxdeepin/go-lib/gsettings"
	"github.com/linuxdeepin/go-lib/xdg/basedir"
	x "github.com/linuxdeepin/go-x11-client"
	"github.com/linuxdeepin/go-x11-client/util/keysyms"
)

//go:generate dbusutil-gen em -type Manager

const (
	// shortcut signals:
	shortcutSignalChanged = "Changed"
	shortcutSignalAdded   = "Added"
	shortcutSignalDeleted = "Deleted"

	gsSchemaKeyboard          = "com.deepin.dde.keyboard"
	gsKeyNumLockState         = "numlock-state"
	gsKeySaveNumLockState     = "save-numlock-state"
	gsKeyShortcutSwitchLayout = "shortcut-switch-layout"
	gsKeyShowCapsLockOSD      = "capslock-toggle"
	gsKeyUpperLayerWLAN       = "upper-layer-wlan"

	gsSchemaSystem         = "com.deepin.dde.keybinding.system"
	gsSchemaSystemPlatform = "com.deepin.dde.keybinding.system.platform"
	gsSchemaSystemEnable   = "com.deepin.dde.keybinding.system.enable"
	gsSchemaMediaKey       = "com.deepin.dde.keybinding.mediakey"
	gsSchemaGnomeWM        = "com.deepin.wrap.gnome.desktop.wm.keybindings"
	gsSchemaSessionPower   = "com.deepin.dde.power"

	customConfigFile = "deepin/dde-daemon/keybinding/custom.ini"
	CapslockKey      = 58
	NumlockKey       = 69
	KeyPress         = 1

	configManagerId = "org.desktopspec.ConfigManager"
)

const ( // power按键事件的响应
	powerActionShutdown int32 = iota
	powerActionSuspend
	powerActionHibernate
	powerActionTurnOffScreen
	powerActionShowUI
)

var _useWayland bool

func setUseWayland(value bool) {
	_useWayland = value
}

type Manager struct {
	service *dbusutil.Service
	// properties
	NumLockState         gsprop.Enum
	ShortcutSwitchLayout gsprop.Uint `prop:"access:rw"`

	conn       *x.Conn
	keySymbols *keysyms.KeySymbols

	gsKeyboard       *gio.Settings
	gsSystem         *gio.Settings
	gsSystemPlatform *gio.Settings
	gsSystemEnable   *gio.Settings
	gsMediaKey       *gio.Settings
	gsGnomeWM        *gio.Settings
	gsPower          *gio.Settings

	enableListenGSettings   bool
	delayNetworkStateChange bool
	dpmsIsOff               bool
	clickNum                uint32
	shortcutCmd             string
	shortcutKey             string
	shortcutKeyCmd          string
	customShortcutManager   *shortcuts.CustomShortcutManager

	lockFront     lockfront.LockFront
	shutdownFront shutdownfront.ShutdownFront

	sessionSigLoop            *dbusutil.SignalLoop
	systemSigLoop             *dbusutil.SignalLoop
	startManager              sessionmanager.StartManager
	sessionManager            sessionmanager.SessionManager
	airplane                  airplanemode.AirplaneMode
	backlightHelper           backlight.Backlight
	keyboard                  inputdevices.Keyboard
	keyboardLayout            string
	wm                        wm.Wm
	waylandOutputMgr          kwayland.OutputManagement
	login1Manager             login1.Manager
	keyEvent                  keyevent.KeyEvent
	specialKeycodeBindingList map[SpecialKeycodeMapKey]func()

	// controllers
	audioController       *AudioController
	mediaPlayerController *MediaPlayerController
	displayController     *DisplayController
	kbdLightController    *KbdLightController
	touchPadController    *TouchPadController

	shortcutManager *shortcuts.ShortcutManager
	// shortcut action handlers
	handlers             []shortcuts.KeyEventFunc
	lastKeyEventTime     time.Time
	lastExecCmdTime      time.Time
	lastMethodCalledTime time.Time
	grabScreenKeystroke  *shortcuts.Keystroke

	// for switch kbd layout
	switchKbdLayoutState SKLState
	sklWaitQuit          chan int

	// dsg config
	wifiControlEnable bool

	configManagerPath dbus.ObjectPath

	//nolint
	signals *struct {
		Added, Deleted, Changed struct {
			id  string
			typ int32
		}

		KeyEvent struct {
			pressed   bool
			keystroke string
		}
	}
}

// SKLState Switch keyboard Layout state
type SKLState uint

const (
	SKLStateNone SKLState = iota
	SKLStateWait
	SKLStateOSDShown
)

func newManager(service *dbusutil.Service) (*Manager, error) {
	setUseWayland(strings.Contains(os.Getenv("XDG_SESSION_TYPE"), "wayland"))
	conn, err := x.NewConn()
	if err != nil {
		return nil, err
	}

	sessionBus := service.Conn()
	sysBus, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}

	var m = Manager{
		service:               service,
		enableListenGSettings: true,
		conn:                  conn,
		keySymbols:            keysyms.NewKeySymbols(conn),
	}

	m.sessionSigLoop = dbusutil.NewSignalLoop(sessionBus, 10)
	m.systemSigLoop = dbusutil.NewSignalLoop(sysBus, 10)

	if _useWayland {
		m.waylandOutputMgr = kwayland.NewOutputManagement(sessionBus)
		m.login1Manager = login1.NewManager(sysBus)
	}

	// 加载dsg配置
	systemConnObj := sysBus.Object(configManagerId, "/")
	err = systemConnObj.Call(configManagerId+".acquireManager", 0, "org.deepin.dde.daemon", "org.deepin.dde.daemon.keybinding", "").Store(&m.configManagerPath)
	if err != nil {
		logger.Warning(err)
	}

	err = dbusutil.NewMatchRuleBuilder().Type("signal").
		PathNamespace(string(m.configManagerPath)).
		Interface("org.desktopspec.ConfigManager.Manager").
		Member("valueChanged").Build().AddTo(sysBus)
	if err != nil {
		logger.Warning(err)
	}

	m.wifiControlEnable = m.getwirelessControlEnable()

	m.init()

	m.gsKeyboard = gio.NewSettings(gsSchemaKeyboard)
	m.NumLockState.Bind(m.gsKeyboard, gsKeyNumLockState)
	m.ShortcutSwitchLayout.Bind(m.gsKeyboard, gsKeyShortcutSwitchLayout)
	m.sessionSigLoop.Start()
	m.systemSigLoop.Start()

	m.initNumLockState(sysBus)

	return &m, nil
}

func (m *Manager) init() {
	sessionBus := m.service.Conn()
	sysBus, _ := dbus.SystemBus()
	m.delayNetworkStateChange = true
	m.dpmsIsOff = false

	// init settings
	m.gsSystem = gio.NewSettings(gsSchemaSystem)
	m.gsSystemPlatform = gio.NewSettings(gsSchemaSystemPlatform)
	m.gsSystemEnable = gio.NewSettings(gsSchemaSystemEnable)
	m.gsMediaKey = gio.NewSettings(gsSchemaMediaKey)
	m.gsPower = gio.NewSettings(gsSchemaSessionPower)
	m.wm = wm.NewWm(sessionBus)
	m.keyEvent = keyevent.NewKeyEvent(sysBus)

	m.shortcutManager = shortcuts.NewShortcutManager(m.conn, m.keySymbols, m.handleKeyEvent)

	// when session is locked, we need handle some keyboard function event
	m.lockFront = lockfront.NewLockFront(sessionBus)
	m.lockFront.InitSignalExt(m.sessionSigLoop, true)
	m.lockFront.ConnectChangKey(func(changKey string) {
		m.handleKeyEventFromLockFront(changKey)
	})

	m.shutdownFront = shutdownfront.NewShutdownFront(sessionBus)
	m.shutdownFront.InitSignalExt(m.sessionSigLoop, true)
	m.shutdownFront.ConnectChangKey(func(changKey string) {
		m.handleKeyEventFromShutdownFront(changKey)
	})

	if _useWayland {
		if shouldUseDDEKwin() {
			m.shortcutManager.AddSpecialToKwin(m.wm)
			m.shortcutManager.AddSystemToKwin(m.gsSystem, m.wm)
			m.shortcutManager.AddMediaToKwin(m.gsMediaKey, m.wm)
			m.shortcutManager.AddKWinForWayland(m.wm)
		} else {
			m.shortcutManager.AddSpecial()
			m.shortcutManager.AddSystem(m.gsSystem, m.gsSystemPlatform, m.gsSystemEnable, m.wm)
			m.shortcutManager.AddMedia(m.gsMediaKey, m.wm)
			m.gsGnomeWM = gio.NewSettings(gsSchemaGnomeWM)
			m.shortcutManager.AddWM(m.gsGnomeWM, m.wm)
		}
	} else {
		m.shortcutManager.AddSystem(m.gsSystem, m.gsSystemPlatform, m.gsSystemEnable, m.wm)
		m.shortcutManager.AddMedia(m.gsMediaKey, m.wm)
		if shouldUseDDEKwin() {
			logger.Debug("Use DDE KWin")
			m.shortcutManager.AddKWin(m.wm)
		} else {
			logger.Debug("Use gnome WM")
			m.gsGnomeWM = gio.NewSettings(gsSchemaGnomeWM)
			m.shortcutManager.AddWM(m.gsGnomeWM, m.wm)
		}
	}

	// init custom shortcuts
	customConfigFilePath := filepath.Join(basedir.GetUserConfigDir(), customConfigFile)
	m.customShortcutManager = shortcuts.NewCustomShortcutManager(customConfigFilePath)
	m.shortcutManager.AddCustom(m.customShortcutManager, m.wm)

	// init controllers
	m.backlightHelper = backlight.NewBacklight(sysBus)
	m.audioController = NewAudioController(sessionBus, m.backlightHelper)
	m.mediaPlayerController = NewMediaPlayerController(m.systemSigLoop, sessionBus)

	m.startManager = sessionmanager.NewStartManager(sessionBus)
	m.airplane = airplanemode.NewAirplaneMode(sysBus)
	m.sessionManager = sessionmanager.NewSessionManager(sessionBus)
	m.keyboard = inputdevices.NewKeyboard(sessionBus)
	m.keyboard.InitSignalExt(m.sessionSigLoop, true)
	err := m.keyboard.CurrentLayout().ConnectChanged(func(hasValue bool, layout string) {
		if !hasValue {
			return
		}
		if m.keyboardLayout != layout {
			m.keyboardLayout = layout
			logger.Debug("keyboard layout changed:", layout)
			m.shortcutManager.NotifyLayoutChanged()
		}
	})

	if err != nil {
		logger.Warning("connect CurrentLayout property changed failed:", err)
	}

	m.displayController = NewDisplayController(m.backlightHelper, sessionBus)
	m.kbdLightController = NewKbdLightController(m.backlightHelper)
	m.touchPadController = NewTouchPadController(sessionBus)

	m.initSpecialKeycodeMap()
	m.keyEvent.InitSignalExt(m.systemSigLoop, true)
	_, err = m.keyEvent.ConnectKeyEvent(m.handleSpecialKeycode)
	if err != nil {
		logger.Warning(err)
	}

	if _useWayland {
		m.initHandlers()
		m.clickNum = 0

		go m.listenGlobalAccel(sessionBus)
		go m.listenKeyboardEvent(sysBus)
		go m.listenMouseEvent(sysBus)
	}
}

var kwinSysActionCmdMap = map[string]string{
	"Launcher":              "launcher",               //Super_L Super_R
	"Terminal":              "terminal",               //<Control><Alt>T
	"Terminal Quake Window": "terminal-quake",         //
	"Lock screen":           "lock-screen",            //super+l
	"Shutdown interface":    "logout",                 //ctrl+alt+del
	"File manager":          "file-manager",           //super+e
	"Screenshot":            "screenshot",             //ctrl+alt+a
	"Full screenshot":       "screenshot-fullscreen",  //print
	"Window screenshot":     "screenshot-window",      //alt+print
	"Delay screenshot":      "screenshot-delayed",     //ctrl+print
	"Disable Touchpad":      "disable-touchpad",       //
	"Switch window effects": "wm-switcher",            //alt+tab
	"turn-off-screen":       "Fast Screen Off",        //<Shift><Super>L
	"Deepin Picker":         "color-picker",           //ctrl+alt+v
	"System Monitor":        "system-monitor",         //ctrl+alt+escape
	"Screen Recorder":       "deepin-screen-recorder", // deepin-screen-recorder ctrl+alt+r
	"Desktop AI Assistant":  "ai-assistant",           // ai-assistant [<Super>Q]q
	"Text to Speech":        "text-to-speech",
	"Speech to Text":        "speech-to-text",
	"Clipboard":             "clipboard",
	"Translation":           "translation",
	"Show/Hide the dock":    "show-dock",

	// cmd
	"Calculator":          "calculator",          // XF86Calculator
	"Search":              "search",              // XF86Search
	"Notification Center": "notification-center", // Meta M

	"ScreenshotScroll": "screenshot-scroll",
	"ScreenshotOcr":    "screenshot-ocr",
	"Global Search":    "global-search",
}

var waylandMediaIdMap = map[string]string{
	"Messenger":         "messenger",           // XF86Messenger
	"Save":              "save",                // XF86Save
	"New":               "new",                 // XF86New
	"WakeUp":            "wake-up",             // XF86WakeUp
	"audio-rewind":      "AudioRewind",         // XF86AudioRewind
	"VolumeMute":        "audio-mute",          // XF86AudioMute  "AudioMute":
	"MonBrightnessUp":   "mon-brightness-up",   // XF86MonBrightnessUp
	"WLAN":              "wlan",                // XF86WLAN
	"AudioMedia":        "audio-media",         // XF86AudioMedia
	"reply":             "Reply",               // XF86Reply
	"favorites":         "Favorites",           // XF86Favorites
	"AudioPlay":         "audio-play",          // XF86AudioPlay
	"AudioMicMute":      "audio-mic-mute",      // XF86AudioMicMute
	"AudioPause":        "audio-pause",         // XF86AudioPause
	"AudioStop":         "audio-stop",          // XF86AudioStop
	"documents":         "Documents",           // XF86Documents
	"game":              "Game",                // XF86Game
	"AudioRecord":       "audio-record",        // XF86AudioRecord
	"Display":           "display",             // XF86Display
	"reload":            "Reload",              // XF86Reload
	"explorer":          "Explorer",            // XF86Explorer
	"calendar":          "Calendar",            // XF86Calendar
	"forward":           "Forward",             // XF86Forward
	"cut":               "Cut",                 // XF86Cut
	"MonBrightnessDown": "mon-brightness-down", // XF86MonBrightnessDown
	"Copy":              "copy",                // XF86Copy
	"Tools":             "tools",               // XF86Tools
	"VolumeUp":          "audio-raise-volume",  // XF86AudioRaiseVolume "AudioRaiseVolume":  "audio-raise-volume",
	"media-close":       "media-Close",         // XF86Close
	"WWW":               "www",                 // XF86WWW
	"HomePage":          "home-page",           // XF86HomePage
	"sleep":             "Sleep",               // XF86Sleep
	"VolumeDown":        "audio-lower-volume",  // XF86AudioLowerVolume  "AudioLowerVolume":  "audio-lower-volume",
	"AudioPrev":         "audio-prev",          // XF86AudioPrev
	"AudioNext":         "audio-next",          // XF86AudioNext
	"Paste":             "paste",               // XF86Paste
	"open":              "Open",                // XF86Open
	"send":              "Send",                // XF86Send
	"my-computer":       "MyComputer",          // XF86MyComputer
	"Mail":              "mail",                // XF86Mail
	"adjust-brightness": "BrightnessAdjust",    // XF86BrightnessAdjust
	"LogOff":            "log-off",             // XF86LogOff
	"pictures":          "Pictures",            // XF86Pictures
	"Terminal":          "terminal",            // XF86Terminal
	"video":             "Video",               // XF86Video
	"Music":             "music",               // XF86Music
	"app-left":          "ApplicationLeft",     // XF86ApplicationLeft
	"app-right":         "ApplicationRight",    // XF86ApplicationRight
	"meeting":           "Meeting",             // XF86Meeting
	"Switch monitors":   "switch-monitors",
	"Numlock":           "numlock",
	"Capslock":          "capslock",
	"Switch kbd layout": "switch-kbd-layout",
}

func (m *Manager) listenGlobalAccel(sessionBus *dbus.Conn) error {
	err := sessionBus.Object("org.kde.kglobalaccel",
		"/component/kwin").AddMatchSignal("org.kde.kglobalaccel.Component", "globalShortcutPressed").Err
	if err != nil {
		logger.Warning(err)
		return err
	}

	m.sessionSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "org.kde.kglobalaccel.Component.globalShortcutPressed",
	}, func(sig *dbus.Signal) {
		if len(sig.Body) > 1 {
			m.shortcutKey = sig.Body[0].(string)
			m.shortcutKeyCmd = sig.Body[1].(string)
			ok := strings.Compare(string("kwin"), m.shortcutKey)
			if ok == 0 {
				logger.Debug("[test global key] get accel sig.Body[1]", sig.Body[1])
				if m.shortcutKeyCmd == "" {
					//+ 把响应一次的逻辑放到协程外执行，防止协程响应延迟
					m.handleKeyEventByWayland(waylandMediaIdMap[m.shortcutKeyCmd])
				} else {
					m.shortcutCmd = shortcuts.GetSystemActionCmd(kwinSysActionCmdMap[m.shortcutKeyCmd])
					if m.shortcutCmd == "" {
						m.shortcutCmd = m.shortcutManager.WaylandCustomShortCutMap[m.shortcutKeyCmd]
					}
					logger.Debug("WaylandCustomShortCutMap", m.shortcutCmd)
					if m.shortcutCmd == "" {
						m.handleKeyEventByWayland(waylandMediaIdMap[m.shortcutKeyCmd])
					} else {
						if strings.HasSuffix(m.shortcutCmd, ".desktop") {
							err := m.runDesktopFile(m.shortcutCmd)
							if err != nil {
								logger.Warning(err)
							}
						} else {
							err := m.execCmd(m.shortcutCmd, true)
							if err != nil {
								logger.Warning(err)
							}
						}
					}
				}
			}
		}
	})

	//+ 监控鼠标移动事件
	err = sessionBus.Object("com.deepin.daemon.KWayland",
		"/com/deepin/daemon/KWayland/Output").AddMatchSignal("com.deepin.daemon.KWayland.Output", "CursorMove").Err
	if err != nil {
		logger.Warning(err)
		return err
	}
	m.sessionSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "com.deepin.daemon.KWayland.Output.CursorMove",
	}, func(sig *dbus.Signal) {
		if len(sig.Body) > 1 {
			if m.dpmsIsOff {
				err := exec.Command("dde_wldpms", "-s", "On").Run()
				if err != nil {
					logger.Warningf("failed to exec dde_wldpms: %s", err)
				} else {
					m.dpmsIsOff = false
				}
			}
		}
	})

	//+ 监控鼠标按下事件
	err = sessionBus.Object("com.deepin.daemon.KWayland",
		"/com/deepin/daemon/KWayland/Output").AddMatchSignal("com.deepin.daemon.KWayland.Output", "ButtonPress").Err
	if err != nil {
		logger.Warning(err)
		return err
	}
	m.sessionSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "com.deepin.daemon.KWayland.Output.ButtonPress",
	}, func(sig *dbus.Signal) {
		if len(sig.Body) > 1 {
			if m.dpmsIsOff {
				err := exec.Command("dde_wldpms", "-s", "On").Run()
				if err != nil {
					logger.Warningf("failed to exec dde_wldpms: %s", err)
				} else {
					m.dpmsIsOff = false
				}
			}
		}
	})
	return nil
}

func (m *Manager) listenKeyboardEvent(systemBus *dbus.Conn) error {
	err := systemBus.Object("com.deepin.daemon.Gesture",
		"/com/deepin/daemon/Gesture").AddMatchSignal("com.deepin.daemon.Gesture", "KeyboardEvent").Err
	if err != nil {
		logger.Warning(err)
		return err
	}
	m.systemSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "com.deepin.daemon.Gesture.KeyboardEvent",
	}, func(sig *dbus.Signal) {
		if len(sig.Body) > 1 {
			key := sig.Body[0].(uint32)
			value := sig.Body[1].(uint32)
			//+ 短按电源键同时出发kwin快捷键逻辑和libinput逻辑有冲突，先屏蔽
			if m.dpmsIsOff && value == 1 && key != 116 {
				logger.Debug("Keyboard:", key, value)
				err := exec.Command("dde_wldpms", "-s", "On").Run()
				if err != nil {
					logger.Warningf("failed to exec dde_wldpms: %s", err)
				} else {
					m.dpmsIsOff = false
				}
			}

			if _useWayland {
				if key == CapslockKey && value == KeyPress {
					m.handleKeyEventByWayland("capslock")
				} else if key == NumlockKey && value == KeyPress {
					m.handleKeyEventByWayland("numlock")
				}
			}
		}
	})

	// 监听dsg配置变化
	m.systemSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "org.desktopspec.ConfigManager.Manager.valueChanged",
	}, func(sig *dbus.Signal) {
		if strings.Contains(string(sig.Name), "org.desktopspec.ConfigManager.Manager.valueChanged") {
			m.wifiControlEnable = m.getwirelessControlEnable()
		}
	})
	return nil
}

func (m *Manager) listenMouseEvent(systemBus *dbus.Conn) error {
	err := systemBus.Object("com.deepin.daemon.Gesture",
		"/com/deepin/daemon/Gesture").AddMatchSignal("com.deepin.daemon.Gesture", "MouseEvent").Err
	if err != nil {
		logger.Warning(err)
		return err
	}
	m.systemSigLoop.AddHandler(&dbusutil.SignalRule{
		Name: "com.deepin.daemon.Gesture.MouseEvent",
	}, func(sig *dbus.Signal) {
		if len(sig.Body) > 1 {
			if m.dpmsIsOff {
				logger.Debug("MouseEvent: dpms on")
				err := exec.Command("dde_wldpms", "-s", "On").Run()
				if err != nil {
					logger.Warningf("failed to exec dde_wldpms: %s", err)
				} else {
					m.dpmsIsOff = false
				}
			}
		}
	})
	return nil
}

// 初始化 NumLock 数字锁定键状态
func (m *Manager) initNumLockState(sysBus *dbus.Conn) {
	// 从 gsettings 读取相关设置
	nlState := NumLockState(m.NumLockState.Get())
	saveStateEnabled := m.gsKeyboard.GetBoolean(gsKeySaveNumLockState)
	if nlState == NumLockUnknown {
		// 判断是否是笔记本, 只根据电池状态，有电池则是笔记本。
		isLaptop := false
		sysPower := power.NewPower(sysBus)
		hasBattery, err := sysPower.HasBattery().Get(0)
		if err != nil {
			logger.Warning("failed to get sysPower HasBattery property:", err)
		} else if hasBattery {
			isLaptop = true
		}

		state := NumLockUnknown
		logger.Debug("isLaptop:", isLaptop)
		if isLaptop {
			// 笔记本，默认关闭。
			state = NumLockOff
		} else {
			// 台式机等，默认开启。
			state = NumLockOn
		}

		if saveStateEnabled {
			// 保存新状态到 gsettings
			m.NumLockState.Set(int32(state))
		}

		err = setNumLockState(m.waylandOutputMgr, m.conn, m.keySymbols, state)
		if err != nil {
			logger.Warning("setNumLockState failed:", err)
		}
	} else {
		if saveStateEnabled {
			err := setNumLockState(m.waylandOutputMgr, m.conn, m.keySymbols, nlState)
			if err != nil {
				logger.Warning("setNumLockState failed:", err)
			}
		}
	}

}

func (m *Manager) handleKeyEventFromLockFront(changKey string) {
	logger.Debugf("Receive LockFront ChangKey Event %s", changKey)
	action := shortcuts.GetAction(changKey)

	// numlock/capslock
	if action.Type == shortcuts.ActionTypeShowNumLockOSD ||
		action.Type == shortcuts.ActionTypeShowCapsLockOSD ||
		action.Type == shortcuts.ActionTypeSystemShutdown {
		if handler := m.handlers[int(action.Type)]; handler != nil {
			handler(nil)
		} else {
			logger.Warning("handler is nil")
		}
	} else {
		cmd, ok := action.Arg.(shortcuts.ActionCmd)
		if !ok {
			logger.Warning(errTypeAssertionFail)
		} else {
			if action.Type == shortcuts.ActionTypeAudioCtrl {
				// audio-mute/audio-lower-volume/audio-raise-volume
				if m.audioController != nil {
					if err := m.audioController.ExecCmd(cmd); err != nil {
						logger.Warning(m.audioController.Name(), "Controller exec cmd err:", err)
					}
				}
			} else if action.Type == shortcuts.ActionTypeDisplayCtrl {
				// mon-brightness-up/mon-brightness-down
				if m.displayController != nil {
					if err := m.displayController.ExecCmd(cmd); err != nil {
						logger.Warning(m.displayController.Name(), "Controller exec cmd err:", err)
					}
				}
			} else if action.Type == shortcuts.ActionTypeTouchpadCtrl {
				// touchpad-toggle/touchpad-on/touchpad-off
				if m.touchPadController != nil {
					if err := m.touchPadController.ExecCmd(cmd); err != nil {
						logger.Warning(m.touchPadController.Name(), "Controller exec cmd err:", err)
					}
				}
			}
		}
	}
}

func (m *Manager) handleKeyEventByWayland(changKey string) {
	action := shortcuts.GetAction(changKey)
	var isWaylandGrabed bool = false
	if _useWayland {
		isWaylandGrabed = true
		if action.Type == shortcuts.ActionTypeShowNumLockOSD || action.Type == shortcuts.ActionTypeShowCapsLockOSD {
			sessionBus, err := dbus.SessionBus()
			if err != nil {
				return
			}
			sessionObj := sessionBus.Object("org.kde.KWin", "/KWin")
			err = sessionObj.Call("org.kde.KWin.xwaylandGrabed", 0).Store(&isWaylandGrabed)
			if err != nil {
				logger.Warning(err)
				return
			}
			logger.Debug("xwaylandGrabed: ", isWaylandGrabed)
		}
	}
	// numlock/capslock
	if action.Type == shortcuts.ActionTypeSystemShutdown {
		var powerPressAction int32
		systemBus, _ := dbus.SystemBus()
		systemPower := power.NewPower(systemBus)
		onBattery, err := systemPower.OnBattery().Get(0)
		if err != nil {
			logger.Error(err)
		}
		if onBattery {
			powerPressAction = m.gsPower.GetEnum("battery-press-power-button")
		} else {
			powerPressAction = m.gsPower.GetEnum("line-power-press-power-button")
		}
		logger.Debug("powerPressAction:", powerPressAction)
		switch powerPressAction {
		case powerActionShutdown:
			m.systemShutdown()
		case powerActionSuspend:
			systemSuspend()
		case powerActionHibernate:
			m.systemHibernate()
		case powerActionTurnOffScreen:
			m.systemTurnOffScreen()
		case powerActionShowUI:
			cmd := "dde-shutdown"
			go func() {
				err := m.execCmd(cmd, false)
				if err != nil {
					logger.Warning("execCmd error:", err)
				}
			}()
		}
	} else if action.Type == shortcuts.ActionTypeShowControlCenter {
		err := m.execCmd("dbus-send --session --dest=com.deepin.dde.ControlCenter  --print-reply /com/deepin/dde/ControlCenter com.deepin.dde.ControlCenter.Show",
			false)
		if err != nil {
			logger.Warning("failed to show control center:", err)
		}

	} else if action.Type == shortcuts.ActionTypeToggleWireless {
		// check if allow set wireless
		// and check if Wifi shortcut effected by DDE software
		if m.gsMediaKey.GetBoolean(gsKeyUpperLayerWLAN) && m.wifiControlEnable {
			enabled, err := m.airplane.WifiEnabled().Get(0)
			if err != nil {
				logger.Warningf("get wireless enabled failed, err: %v", err)
				return
			}
			err = m.airplane.EnableWifi(0, !enabled)
			if err != nil {
				logger.Warningf("set wireless enabled failed, err: %v", err)
				return
			}
		}
	} else if action.Type == shortcuts.ActionTypeShowNumLockOSD {
		var state NumLockState
		if !isWaylandGrabed {
			if _useWayland {
				sessionBus, err := dbus.SessionBus()
				if err != nil {
					return
				}
				time.Sleep(200 * time.Millisecond) //+ 添加200ms延时，保证在dde-system-daemon中先获取状态；
				sessionObj := sessionBus.Object("org.kde.KWin", "/Xkb")
				var ret int32
				err = sessionObj.Call("org.kde.kwin.Xkb.getLeds", 0).Store(&ret)
				if err != nil {
					logger.Warning(err)
					return
				}
				if 0 == (ret & 0x1) {
					state = NumLockOff
				} else {
					state = NumLockOn
				}
			} else {
				var err error
				state, err = queryNumLockState(m.conn)
				if err != nil {
					logger.Warning(err)
					return
				}
			}

			save := m.gsKeyboard.GetBoolean(gsKeySaveNumLockState)

			switch state {
			case NumLockOn:
				if save {
					m.NumLockState.Set(int32(NumLockOn))
				}
				showOSD("NumLockOn")
			case NumLockOff:
				if save {
					m.NumLockState.Set(int32(NumLockOff))
				}
				showOSD("NumLockOff")
			}
		}
	} else if action.Type == shortcuts.ActionTypeShowCapsLockOSD {
		if !m.shouldShowCapsLockOSD() {
			return
		}

		if !isWaylandGrabed {
			var state CapsLockState
			if _useWayland {
				sessionBus, err := dbus.SessionBus()
				if err != nil {
					return
				}
				time.Sleep(200 * time.Millisecond) //+ 添加200ms延时，保证在dde-system-daemon中先获取状态；
				sessionObj := sessionBus.Object("org.kde.KWin", "/Xkb")
				var ret int32
				err = sessionObj.Call("org.kde.kwin.Xkb.getLeds", 0).Store(&ret)
				if err != nil {
					logger.Warning(err)
					return
				}
				if 0 == (ret & 0x2) {
					state = CapsLockOff
				} else {
					state = CapsLockOn
				}
			} else {
				state, err := queryCapsLockState(m.conn)
				if err != nil {
					logger.Warning(err)
					return
				}
				logger.Debug("caps:", state)
			}

			switch state {
			case CapsLockOff:
				showOSD("CapsLockOff")
			case CapsLockOn:
				showOSD("CapsLockOn")
			}
		}
	} else if action.Type == shortcuts.ActionTypeSwitchKbdLayout {
		switch m.switchKbdLayoutState {
		case SKLStateNone:
			m.switchKbdLayoutState = SKLStateWait
			go m.sklWait()

		case SKLStateWait:
			m.switchKbdLayoutState = SKLStateOSDShown
			m.terminateSKLWait()
			showOSD("SwitchLayout")

		case SKLStateOSDShown:
			showOSD("SwitchLayout")
		}
	} else {
		cmd, ok := action.Arg.(shortcuts.ActionCmd)
		if !ok {
			logger.Warning(errTypeAssertionFail)
		} else {
			if action.Type == shortcuts.ActionTypeAudioCtrl {
				// audio-mute/audio-lower-volume/audio-raise-volume
				if m.audioController != nil {
					if err := m.audioController.ExecCmd(cmd); err != nil {
						logger.Warning(m.audioController.Name(), "Controller exec cmd err:", err)
					}
				}
			} else if action.Type == shortcuts.ActionTypeDisplayCtrl {
				// mon-brightness-up/mon-brightness-down
				if m.displayController != nil {
					if err := m.displayController.ExecCmd(cmd); err != nil {
						logger.Warning(m.displayController.Name(), "Controller exec cmd err:", err)
					}
				}
			} else if action.Type == shortcuts.ActionTypeTouchpadCtrl {
				// touchpad-toggle/touchpad-on/touchpad-off
				if m.touchPadController != nil {
					if err := m.touchPadController.ExecCmd(cmd); err != nil {
						logger.Warning(m.touchPadController.Name(), "Controller exec cmd err:", err)
					}
				}
			} else if action.Type == shortcuts.ActionTypeSystemShutdown {

			} else if action.Type == shortcuts.ActionTypeMediaPlayerCtrl {
				//增蓝牙耳机快捷键的处理
				if cmd == shortcuts.MediaPlayerPlay {
					m.clickNum = m.clickNum + 1
					if m.clickNum == 1 {
						time.AfterFunc(time.Millisecond*600, func() {
							m.playMeadiaByHeadphone()
						})
					}
				} else {
					if m.mediaPlayerController != nil {
						err := m.mediaPlayerController.ExecCmd(cmd)
						if err != nil {
							logger.Warning(m.mediaPlayerController.Name(), "Controller exec cmd err:", err)
						}
					}
				}

			}
		}
	}
}

func getMediaPlayAction(num uint32) shortcuts.ActionCmd {
	var cmd shortcuts.ActionCmd = shortcuts.MediaPlayerPlay
	if num == 2 {
		cmd = shortcuts.MediaPlayerNext
	} else if num == 3 {
		cmd = shortcuts.MediaPlayerPrevious
	} else {
		cmd = shortcuts.MediaPlayerPlay
	}
	return cmd
}

func (m *Manager) playMeadiaByHeadphone() {
	cmd := getMediaPlayAction(m.clickNum)
	m.clickNum = 0
	if m.mediaPlayerController != nil {
		err := m.mediaPlayerController.ExecCmd(cmd)
		if err != nil {
			logger.Warning(m.mediaPlayerController.Name(), "Controller exec cmd err:", err)
		}
	}
	return
}

func (m *Manager) handleKeyEventFromShutdownFront(changKey string) {
	logger.Debugf("handleKeyEvent %s from ShutdownFront", changKey)
	action := shortcuts.GetAction(changKey)
	if action.Type == shortcuts.ActionTypeSystemShutdown {
		if handler := m.handlers[int(action.Type)]; handler != nil {
			handler(nil)
		} else {
			logger.Warning("handler is nil")
		}
	}
}

func (m *Manager) destroy() {
	err := m.service.StopExport(m)
	if err != nil {
		logger.Warning("stop export failed:", err)
	}

	if m.shortcutManager != nil {
		m.shortcutManager.Destroy()
		m.shortcutManager = nil
	}

	// destroy settings
	if m.gsSystem != nil {
		m.gsSystem.Unref()
		m.gsSystem = nil
	}

	if m.gsMediaKey != nil {
		m.gsMediaKey.Unref()
		m.gsMediaKey = nil
	}

	if m.gsGnomeWM != nil {
		m.gsGnomeWM.Unref()
		m.gsGnomeWM = nil
	}

	if m.audioController != nil {
		m.audioController.Destroy()
		m.audioController = nil
	}

	if m.mediaPlayerController != nil {
		m.mediaPlayerController.Destroy()
		m.mediaPlayerController = nil
	}

	if m.keyboard != nil {
		m.keyboard.RemoveHandler(proxy.RemoveAllHandlers)
		m.keyboard = nil
	}

	if m.keyEvent != nil {
		m.keyEvent.RemoveHandler(proxy.RemoveAllHandlers)
		m.keyEvent = nil
	}

	if m.sessionSigLoop != nil {
		m.sessionSigLoop.Stop()
		m.sessionSigLoop = nil
	}

	if m.systemSigLoop != nil {
		m.systemSigLoop.Stop()
		m.systemSigLoop = nil
	}

	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}
}

func (m *Manager) handleKeyEvent(ev *shortcuts.KeyEvent) {
	const minKeyEventInterval = 200 * time.Millisecond
	now := time.Now()
	duration := now.Sub(m.lastKeyEventTime)
	logger.Debug("duration:", duration)
	if 0 < duration && duration < minKeyEventInterval {
		logger.Debug("handleKeyEvent ignore key event")
		return
	}
	m.lastKeyEventTime = now

	logger.Debugf("handleKeyEvent ev: %#v", ev)
	action := ev.Shortcut.GetAction()
	shortcutId := ev.Shortcut.GetId()
	logger.Debugf("shortcut id: %s, type: %v, action: %#v",
		shortcutId, ev.Shortcut.GetType(), action)
	if action == nil {
		logger.Warning("action is nil")
		return
	}

	if handler := m.handlers[int(action.Type)]; handler != nil {
		handler(ev)
	} else {
		logger.Warning("handler is nil")
	}
}

func (m *Manager) emitShortcutSignal(signalName string, shortcut shortcuts.Shortcut) {
	logger.Debug("emit DBus signal", signalName, shortcut.GetId(), shortcut.GetType())
	err := m.service.Emit(m, signalName, shortcut.GetId(), shortcut.GetType())
	if err != nil {
		logger.Warning(err)
	}
}

func (m *Manager) enableListenGSettingsChanged(val bool) {
	m.enableListenGSettings = val
}

func (m *Manager) listenGSettingsChanged(schema string, settings *gio.Settings, type0 int32) {
	gsettings.ConnectChanged(schema, "*", func(key string) {
		if !m.enableListenGSettings {
			return
		}

		shortcut := m.shortcutManager.GetByIdType(key, type0)
		if shortcut == nil {
			return
		}

		keystrokes := settings.GetStrv(key)
		m.shortcutManager.ModifyShortcutKeystrokes(shortcut, shortcuts.ParseKeystrokes(keystrokes))
		m.emitShortcutSignal(shortcutSignalChanged, shortcut)
	})
}

func (m *Manager) listenSystemEnableChanged() {
	gsettings.ConnectChanged(gsSchemaSystemEnable, "*", func(key string) {
		if !m.enableListenGSettings {
			return
		}

		if m.shortcutManager.CheckSystem(m.gsSystemPlatform, m.gsSystemEnable, key) {
			m.shortcutManager.AddSystemById(m.gsSystem, m.wm, key)
		} else {
			m.shortcutManager.DelSystemById(key)
		}
	})
}

func (m *Manager) listenSystemPlatformChanged() {
	gsettings.ConnectChanged(gsSchemaSystemPlatform, "*", func(key string) {
		if !m.enableListenGSettings {
			return
		}

		if m.shortcutManager.CheckSystem(m.gsSystemPlatform, m.gsSystemEnable, key) {
			m.shortcutManager.AddSystemById(m.gsSystem, m.wm, key)
		} else {
			m.shortcutManager.DelSystemById(key)
		}
	})
}

func (m *Manager) execCmd(cmd string, viaStartdde bool) error {
	if cmd == "" {
		logger.Debug("cmd is empty")
		return nil
	}
	if strings.HasPrefix(cmd, "dbus-send ") || !viaStartdde {
		logger.Debug("run cmd:", cmd)
		// #nosec G204
		return exec.Command("/bin/sh", "-c", cmd).Run()
	}

	logger.Debug("startdde run cmd:", cmd)
	return m.startManager.RunCommand(0, "/bin/sh", []string{"-c", cmd})
}

func (m *Manager) runDesktopFile(desktop string) error {
	return m.startManager.LaunchApp(0, desktop, 0, []string{})
}

func (m *Manager) eliminateKeystrokeConflict() {
	for _, ks := range m.shortcutManager.ConflictingKeystrokes {
		shortcut := ks.Shortcut
		logger.Infof("eliminate conflict shortcut: %s keystroke: %s",
			ks.Shortcut.GetUid(), ks)
		err := m.DeleteShortcutKeystroke(shortcut.GetId(), shortcut.GetType(), ks.String())
		if err != nil {
			logger.Warning("delete shortcut keystroke failed:", err)
		}
	}

	m.shortcutManager.ConflictingKeystrokes = nil
	m.shortcutManager.EliminateConflictDone = true
}

func (m *Manager) getwirelessControlEnable() bool {
	systemConn, err := dbus.SystemBus()
	if err != nil {
		return true
	}
	systemConnObj := systemConn.Object("org.desktopspec.ConfigManager", m.configManagerPath)
	var value bool
	err = systemConnObj.Call("org.desktopspec.ConfigManager.Manager.value", 0, "wirelessControlEnable").Store(&value)
	if err != nil {
		logger.Warning(err)
		return false
	}
	return value
}
