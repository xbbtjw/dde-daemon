/*
 * Copyright (C) 2014 ~ 2018 Deepin Technology Co., Ltd.
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

package audio

import (
	"time"

	"github.com/linuxdeepin/dde-daemon/loader"
	"github.com/linuxdeepin/go-lib/dbusutil"
	"github.com/linuxdeepin/go-lib/log"
	"golang.org/x/xerrors"
)

var (
	logger = log.NewLogger("daemon/audio")
)

func init() {
	loader.Register(NewModule(logger))
}

type Module struct {
	*loader.ModuleBase
	audio *Audio
}

func NewModule(logger *log.Logger) *Module {
	var d = new(Module)
	d.ModuleBase = loader.NewModuleBase("audio", d, logger)
	return d
}

func (*Module) GetDependencies() []string {
	return []string{}
}

func (m *Module) start() error {
	err := startPulseaudio(5) // 为了保证蓝牙模块依赖audio模块,并且audio模块启动pulseaudio完成.
	if err != nil {
		err = xerrors.Errorf("failed to start pulseaudio: %w", err)
		logger.Warning(err)
		return err
	}

	service := loader.GetService()
	m.audio = newAudio(service)
	err = m.audio.init()
	if err != nil {
		logger.Warning("failed to init audio module:", err)
		return nil
	}

	err = service.Export(dbusPath, m.audio, m.audio.syncConfig)
	if err != nil {
		return err
	}
	err = service.RequestName(dbusServiceName)
	if err != nil {
		return err
	}

	so := service.GetServerObject(m.audio)
	err = so.SetWriteCallback(m.audio, "ReduceNoise", m.audio.writeReduceNoise)

	err = m.audio.syncConfig.Register()
	if err != nil {
		logger.Warning("failed to register for deepin sync:", err)
	}
	return nil
}

func (m *Module) Start() error {
	if m.audio != nil {
		return nil
	}
	waitSoundThemePlayerExit()
	err := m.start()
	if err != nil {
		logger.Warning(err)
	}
	return nil
}

func (m *Module) Stop() error {
	if m.audio == nil {
		return nil
	}

	m.audio.destroy()

	service := loader.GetService()
	err := service.StopExport(m.audio)
	if err != nil {
		logger.Warning(err)
	}

	err = service.ReleaseName(dbusServiceName)
	if err != nil {
		logger.Warning(err)
	}

	m.audio = nil
	return nil
}

func waitSoundThemePlayerExit() {
	srv, err := dbusutil.NewSystemService()
	if err != nil {
		logger.Warning("Failed to connect system bus:", err)
		return
	}

	for {
		var owner string
		owner, err = srv.GetNameOwner("com.deepin.api.SoundThemePlayer")
		if err != nil {
			return
		}
		logger.Info("Owner:", owner)
		time.Sleep(time.Millisecond * 800)
	}
}
