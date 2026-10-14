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
  C.Project,
  C.CommitSha1,
  C.ChangeID,
  C.Subject,
  C.CommitType,
  C.Email,
  C.CommitTime AS CommitTimeRaw,
  C.FilesChanged,
  C.Insertions,
  C.Deletions
FROM
  COMMITS AS C
WHERE
  (
    "ALL" IN (${DownstreamName:sqlstring})
    OR DownstreamName IN (${DownstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${Project:sqlstring})
    OR Project IN (${Project:sqlstring})
  )