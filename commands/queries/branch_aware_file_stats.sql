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
  BFT.Project,
  BFT.CommitSha1,
  BCT.MergeBase,
  CT.Subject,
  BFT.Path,
  BFT.NetInsertions,
  BFT.NetDeletions,
  BCT.ResidentTime AS ResidentTimeRaw,
  BFT.TotalDeltas
FROM
  BranchFiles AS BFT
INNER JOIN BranchCommits AS BCT
  ON BFT.DownstreamName = BCT.DownstreamName
  AND BFT.DownstreamBranch = BCT.DownstreamBranch
  AND BFT.UpstreamName = BCT.UpstreamName
  AND BFT.UpstreamBranch = BCT.UpstreamBranch
  AND BFT.Project = BCT.Project
  AND BFT.CommitSha1 = BCT.CommitSha1
INNER JOIN Commits AS CT
  ON BFT.DownstreamName = CT.DownstreamName
  AND BFT.Project = CT.Project
  AND BFT.CommitSha1 = CT.CommitSha1
WHERE
  (
    "ALL" IN (${DownstreamName:sqlstring})
    OR BFT.DownstreamName IN (${DownstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${DownstreamBranch:sqlstring})
    OR BFT.DownstreamBranch IN (${DownstreamBranch:sqlstring})
  )
  AND (
    "ALL" IN (${UpstreamName:sqlstring})
    OR BFT.UpstreamName IN (${UpstreamName:sqlstring})
  )
  AND (
    "ALL" IN (${UpstreamBranch:sqlstring})
    OR BFT.UpstreamBranch IN (${UpstreamBranch:sqlstring})
  )
  AND (
    "ALL" IN (${Project:sqlstring})
    OR BFT.Project IN (${Project:sqlstring})
  )
  AND (
    "ALL" IN (${Path:sqlstring})
    OR BFT.Path IN (${Path:sqlstring})
  )