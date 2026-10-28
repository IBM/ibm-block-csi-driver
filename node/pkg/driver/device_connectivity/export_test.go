/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package device_connectivity

// This file is compiled only for tests. It lives in the production package so it can reach
// unexported identifiers, and re-exports them to the external device_connectivity_test package.

import "context"

type ActiveSessionForTest struct {
	SourceIQN string
	HostNum   int
}

func (r *OsDeviceConnectivityIscsi) IscsiCmdForTest(args ...string) (string, error) {
	return r.iscsiCmd(args...)
}

func (r *OsDeviceConnectivityIscsi) IscsiDiscoverForTest(ctx context.Context, portal string) error {
	return r.iscsiDiscover(ctx, portal)
}

func (r *OsDeviceConnectivityIscsi) IscsiLoginForTest(ctx context.Context, targetName, portal string) error {
	return r.iscsiLogin(ctx, targetName, portal)
}

func (r *OsDeviceConnectivityIscsi) IscsiGetRawSessionsForTest(ctx context.Context) ([]string, error) {
	return r.iscsiGetRawSessions(ctx)
}

func (r *OsDeviceConnectivityIscsi) GetAllSessionsForTest(ctx context.Context) (map[string]map[string]bool, error) {
	return r.getAllSessions(ctx)
}

func (r *OsDeviceConnectivityIscsi) ParseRawSessionsForTest(lines []string) (map[string]map[string]bool, error) {
	return r.parseRawSessions(lines)
}

func (r *OsDeviceConnectivityIscsi) FilterLoggedInForTest(ctx context.Context, portalsByTarget map[string][]string) (map[string][]string, error) {
	return r.filterLoggedIn(ctx, portalsByTarget)
}

func (r *OsDeviceConnectivityIscsi) DiscoverAndLoginForTest(ctx context.Context, portalsByTarget map[string][]string) error {
	return r.discoverAndLogin(ctx, portalsByTarget)
}

func (r *OsDeviceConnectivityIscsi) LoadRelevantTargetsForTest(ctx context.Context, requested map[string][]string) (map[string]map[string]bool, error) {
	return r.loadRelevantTargets(ctx, requested)
}

func (r *OsDeviceConnectivityIscsi) NormalizePortalForTest(portal string) string {
	return r.normalizePortal(portal)
}

func (r *OsDeviceConnectivityIscsi) ExtractHostFromDeviceLinkForTest(sessionPath string) (int, error) {
	return r.extractHostFromDeviceLink(sessionPath)
}

func (r *OsDeviceConnectivityIscsi) ParseActiveSessionsForTest(ctx context.Context) ([]ActiveSessionForTest, error) {
	sessions, err := r.parseActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	var result []ActiveSessionForTest
	for _, s := range sessions {
		result = append(result, ActiveSessionForTest{SourceIQN: s.sourceIQN, HostNum: s.hostNum})
	}
	return result, nil
}

func (r *OsDeviceConnectivityIscsi) GetInitiatorIQNForTest(sessionPath, hostName string) (string, error) {
	return r.getInitiatorIQN(sessionPath, hostName)
}

func (r *OsDeviceConnectivityIscsi) UpdateHostIDsForTest(ctx context.Context, hostIDs map[int]bool) {
	r.updateHostIDs(ctx, hostIDs)
}

func (r *OsDeviceConnectivityIscsi) ReadSysfsForTest(path string) (string, error) {
	return r.readSysfs(path)
}

func (r *OsDeviceConnectivityIscsi) DiscoveryScopeKeyForTest(portal string) string {
	return r.getDiscoveryScopeKey(portal)
}

func CleanSysfsDataForTest(data []byte) string {
	return cleanSysfsData(data)
}
