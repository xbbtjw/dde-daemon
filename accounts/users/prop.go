/*
 * Copyright (C) 2013 ~ 2018 Deepin Technology Co., Ltd.
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

package users

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	libdate "github.com/rickb777/date"
)

var (
	errInvalidParam = fmt.Errorf("Invalid or empty parameter")
)

var (
	groupFileTimestamp int64 = 0
	groupFileInfo            = make(map[string]GroupInfo)
	groupFileLocker    sync.Mutex

	groupNameNoPasswdLogin = "nopasswdlogin"
)

type CacheProviderFn func(filename string) (interface{}, error)

func GetShadowInfo(username string) (*ShadowInfo, error) {
	originInfo, err := getSpwd(username)
	if err != nil {
		return nil, err
	}
	sInfo := ShadowInfo{
		Name:       originInfo.Name,
		LastChange: originInfo.LastChange,
		MaxDays:    originInfo.MaxDays,
	}
	shadowPwdp := originInfo.ShadowPwdp
	if len(shadowPwdp) == 0 {
		sInfo.Status = PasswordStatusNoPassword
	} else if []byte(shadowPwdp)[0] == '!' || []byte(shadowPwdp)[0] == '*' {
		sInfo.Status = PasswordStatusLocked
	} else {
		sInfo.Status = PasswordStatusUsable
	}
	return &sInfo, nil
}

func IsPasswordExpired(username string) (bool, error) {
	shadowInfo, err := GetShadowInfo(username)
	if err != nil {
		return false, err
	}

	today := libdate.TodayUTC()
	return isPasswordExpired(shadowInfo, today), nil
}

func isPasswordExpired(shadowInfo *ShadowInfo, today libdate.Date) bool {
	if shadowInfo.LastChange == 0 {
		// must change password
		return true
	}
	if shadowInfo.MaxDays == -1 {
		// never expire
		return false
	}
	expireDate := libdate.New(1970, 1, 1).Add(
		libdate.PeriodOfDays(shadowInfo.LastChange + shadowInfo.MaxDays))
	return today.After(expireDate)
}

const CommentFieldsLen = 5

// CommentInfo is passwd file user comment info
type CommentInfo [CommentFieldsLen]string

func newCommentInfo(comment string) *CommentInfo {
	var ci CommentInfo
	parts := strings.Split(comment, ",")

	// length is min(CommentFieldsLen, len(parts))
	length := len(parts)
	if length > CommentFieldsLen {
		length = CommentFieldsLen
	}

	copy(ci[:], parts[:length])
	return &ci
}

func (ci *CommentInfo) String() string {
	return strings.Join(ci[:], ",")
}

func (ci *CommentInfo) FullName() string {
	return ci[0]
}

func (ci *CommentInfo) SetFullName(value string) {
	ci[0] = value
}

func isCommentFieldValid(name string) bool {
	return !strings.ContainsAny(name, ",=:\n")
}

func ModifyFullName(fullName, username string) error {
	if !isCommentFieldValid(fullName) {
		return errors.New("invalid full name")
	}

	user, err := GetUserInfoByName(username)
	if err != nil {
		return err
	}
	comment := user.Comment()
	comment.SetFullName(fullName)
	user.comment = comment.String()
	err = user.checkLength()
	if err != nil {
		return err
	}

	return modifyComment(comment.String(), username)
}

func modifyComment(comment, username string) error {
	cmd := exec.Command(userCmdModify, "-c", comment, username)
	return cmd.Run()
}

func ModifyHome(dir, username string) error {
	if len(dir) == 0 {
		return errInvalidParam
	}

	user, err := GetUserInfoByName(username)
	if err != nil {
		return err
	}
	user.Home = dir
	err = user.checkLength()
	if err != nil {
		return err
	}

	return doAction(userCmdModify, []string{"-m", "-d", dir, username})
}

func ModifyShell(shell, username string) error {
	if len(shell) == 0 {
		return errInvalidParam
	}

	user, err := GetUserInfoByName(username)
	if err != nil {
		return err
	}
	user.Shell = shell
	err = user.checkLength()
	if err != nil {
		return err
	}

	return doAction(userCmdModify, []string{"-s", shell, username})
}

func ModifyPasswd(words, username string) error {
	if len(words) == 0 {
		return errInvalidParam
	}

	return updatePasswd(words, username)
}

func ModifyMaxPasswordAge(username string, nDays int) error {
	return doAction(cmdChAge, []string{"-M", strconv.Itoa(nDays), username})
}

const (
	// Same as the abbreviation in `passwd --status`
	PasswordStatusUsable     = "P"
	PasswordStatusNoPassword = "NP"
	PasswordStatusLocked     = "L"
)

func IsAutoLoginUser(username string) bool {
	name, _ := GetAutoLoginUser()
	return name == username
}

func IsAdminUser(username string) bool {
	admins, err := getAdminUserList(userFileGroup, userFileSudoers)
	if err != nil {
		return false
	}

	return isStrInArray(username, admins)
}

func CanNoPasswdLogin(username string) bool {
	return isUserInGroup(username, groupNameNoPasswdLogin)
}

func EnableNoPasswdLogin(username string, enabled bool) error {
	if !isGroupExists(groupNameNoPasswdLogin) {
		_ = doAction("groupadd", []string{"-r", groupNameNoPasswdLogin})
	}

	var err error
	exists := isUserInGroup(username, groupNameNoPasswdLogin)
	if enabled {
		if !exists {
			err = doAction(userCmdGroup, []string{"-a", username, groupNameNoPasswdLogin})
		}
	} else {
		if exists {
			err = doAction(userCmdGroup, []string{"-d", username, groupNameNoPasswdLogin})
		}
	}
	return err
}

func getAdminUserList(fileGroup, fileSudoers string) ([]string, error) {
	groups, users, err := getAdmGroupAndUser(fileSudoers)
	if err != nil {
		return nil, err
	}

	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()
	infos, err := getGroupInfoWithCache(fileGroup)
	if err != nil {
		return nil, err
	}

	for _, group := range groups {
		v, ok := infos[group]
		if !ok {
			continue
		}
		users = append(users, v.Users...)
	}
	return users, nil
}

var (
	_admGroups       []string
	_admUsers        []string
	_admTimestampMap = make(map[string]int64)
)

// get adm group and user from '/etc/sudoers'
func getAdmGroupAndUser(file string) ([]string, []string, error) {
	finfo, err := os.Stat(file)
	if err != nil {
		return nil, nil, err
	}
	timestamp := finfo.ModTime().Unix()
	if t, ok := _admTimestampMap[file]; ok && t == timestamp {
		return _admGroups, _admUsers, nil
	}

	fr, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	defer fr.Close()

	var (
		groups  []string
		users   []string
		scanner = bufio.NewScanner(fr)
	)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if line[0] == '#' || !strings.Contains(line, `ALL=(ALL`) {
			continue
		}

		array := strings.Split(line, "ALL")
		// admin group
		if line[0] == '%' {
			// deepin: %sudo\tALL=(ALL:ALL) ALL
			// archlinux: %wheel ALL=(ALL) ALL
			array = strings.Split(array[0], "%")
			tmp := strings.TrimRight(array[1], "\t")
			groups = append(groups, strings.TrimSpace(tmp))
		} else {
			// admin user
			// deepin: root\tALL=(ALL:ALL) ALL
			// archlinux: root ALL=(ALL) ALL
			tmp := strings.TrimRight(array[0], "\t")
			users = append(users, strings.TrimSpace(tmp))
		}
	}
	_admGroups, _admUsers = groups, users
	_admTimestampMap[file] = timestamp
	return groups, users, nil
}

func isGroupExists(group string) bool {
	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()
	infos, err := getGroupInfoWithCache(userFileGroup)
	if err != nil {
		return false
	}
	_, ok := infos[group]
	return ok
}

func isUserInGroup(user, group string) bool {
	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()
	infos, err := getGroupInfoWithCache(userFileGroup)
	if err != nil {
		return false
	}
	v, ok := infos[group]
	if !ok {
		return false
	}
	return isStrInArray(user, v.Users)
}

func GetUserGroups(user, gid string) ([]string, error) {
	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()
	infos, err := getGroupInfoWithCache(userFileGroup)
	if err != nil {
		return nil, err
	}

	var result []string
	for groupName, groupInfo := range infos {
		if groupInfo.Gid == gid {
			result = append(result, groupName)
			continue
		}
		for _, u := range groupInfo.Users {
			if u == user {
				result = append(result, groupName)
				break
			}
		}
	}
	sort.Strings(result)
	return result, nil
}

func GetAllGroups() ([]string, error) {
	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()
	infos, err := getGroupInfoWithCache(userFileGroup)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(infos))
	idx := 0
	for groupName := range infos {
		result[idx] = groupName
		idx++
	}
	sort.Strings(result)
	return result, nil
}

func getGroupInfoWithCache(file string) (map[string]GroupInfo, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if groupFileTimestamp == info.ModTime().UnixNano() &&
		len(groupFileInfo) != 0 {
		return groupFileInfo, nil
	}

	content, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	groupFileTimestamp = info.ModTime().UnixNano()
	groupFileInfo = parseGroup(content)
	return groupFileInfo, nil
}

type GroupInfo struct {
	Name  string
	Gid   string
	Users []string
}

func parseGroup(data []byte) map[string]GroupInfo {
	result := make(map[string]GroupInfo)
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		items := bytes.Split(line, []byte{':'})
		if len(items) != itemLenGroup {
			continue
		}

		var gInfo GroupInfo
		gInfo.Name = string(items[0])
		gInfo.Gid = string(items[2])
		gInfo.Users = strings.Split(string(items[3]), ",")
		result[gInfo.Name] = gInfo
	}

	return result
}

func getGroupByGid(gid string) (*GroupInfo, error) {
	groupFileLocker.Lock()
	defer groupFileLocker.Unlock()

	gInfos, err := getGroupInfoWithCache(userFileGroup)
	if err != nil {
		return nil, err
	}

	for _, gInfo := range gInfos {
		if gInfo.Gid == gid {
			return &gInfo, nil
		}
	}
	return nil, fmt.Errorf("not found group with gid %s", gid)
}

type ShadowInfo struct {
	Name       string
	LastChange int
	MaxDays    int
	Status     string // password status
}
