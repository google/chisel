-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     https://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

SELECT
  FT.Project,
  FT.CommitSha1,
  CT.Subject,
  FT.Path,
  FT.Insertions,
  FT.Deletions,
  CT.Email,
  CT.CommitTime AS CommitTimeRaw
FROM
  Files AS FT
INNER JOIN Commits AS CT
  ON FT.DownstreamName = CT.DownstreamName
  AND FT.Project = CT.Project
  AND FT.CommitSha1 = CT.CommitSha1
WHERE
  (
    "ALL" IN (${DownstreamName:sqlstring})
    OR FT.DownstreamName IN (${DownstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${Project:sqlstring})
    OR FT.Project IN (${Project:sqlstring})
  )
  AND (
    "ALL" IN (${Path:sqlstring})
    OR FT.Path IN (${Path:sqlstring})
  )