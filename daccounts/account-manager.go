/**
 * Copyright (c) 2011 ~ 2013 Deepin, Inc.
 *               2011 ~ 2013 jouyouyun
 *
 * Author:      jouyouyun <jouyouwen717@gmail.com>
 * Maintainer:  jouyouyun <jouyouwen717@gmail.com>
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <http://www.gnu.org/licenses/>.
 **/

package main

import (
	"dlib/dbus"
	"strings"
)

func (m *Manager) ListCachedUsers() []string {
	objects := _accountInface.ListCachedUsers()

	userList := []string{}
	for _, v := range objects {
		userList = append(userList, string(v))
	}

	return userList
}

func (m *Manager) CreateUser(name, fullname string, accountType int32) string {
	path := _accountInface.CreateUser(name, fullname, accountType)

	return ConvertPath(string(path))
}

func (m *Manager) DeleteUser(id int64, removeFiles bool) {
	_accountInface.DeleteUser(id, removeFiles)
}

func NewAccountManager() *Manager {
	m := &Manager{}

	_accountInface.ConnectUserAdded(func(user dbus.ObjectPath) {
		NewAccountUserManager(string(user))
		m.UserAdded(string(user))
	})

	_accountInface.ConnectUserDeleted(func(user dbus.ObjectPath) {
		DeleteUserManager(string(user))
		m.UserDeleted(string(user))
	})

	return m
}

/* Convert freedesktop path to deepin path */
func ConvertPath(path string) string {
	strs := strings.Split(path, "/")
	l := len(strs)
	if l <= 0 {
		return ""
	}

	userID := strs[l-1]

	return _USER_PATH + userID
}
