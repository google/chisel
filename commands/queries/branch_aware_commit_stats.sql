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
  BC.CommitSha1,
  BC.MergeBase,
  C.ChangeID,
  C.Subject,
  C.Email,
  C.CommitType,
  BC.Project,
  BC.IsFirstParent,
  BC.ResidentTime AS ResidentTimeRaw,
  BC.NetFilesChanged,
  BC.NetInsertions,
  BC.NetDeletions,
  BC.TotalDeltas
FROM
  BranchCommits AS BC
INNER JOIN Commits AS C
  ON BC.DownstreamName = C.DownstreamName
  AND BC.Project = C.Project
  AND BC.CommitSha1 = C.CommitSha1
WHERE
  (
    "ALL" IN (${DownstreamName:sqlstring})
    OR BC.DownstreamName IN (${DownstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${DownstreamBranch:sqlstring})
    OR BC.DownstreamBranch IN (${DownstreamBranch:sqlstring})
  )
  AND (
    "ALL" IN (${UpstreamName:sqlstring})
    OR BC.UpstreamName IN (${UpstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${UpstreamBranch:sqlstring})
    OR BC.UpstreamBranch IN (${UpstreamBranch:sqlstring})
  )
  AND (
    "ALL" IN (${Project:sqlstring})
    OR BC.Project IN (${Project:sqlstring})
  )
  AND (
    (${FirstParent:sqlstring} = "true" AND IsFirstParent = 1)
    OR (${FirstParent:sqlstring} = "false" AND IsFirstParent = 0)
    OR (${FirstParent:sqlstring} = "ALL")
  )