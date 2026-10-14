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
  BC.Project,
  BC.NetInsertions,
  BC.NetDeletions,
  BC.NetFilesChanged
FROM
  BranchCommits AS BC
INNER JOIN (
    SELECT
      BC.DownstreamName,
      BC.DownstreamBranch,
      BC.UpstreamName,
      BC.UpstreamBranch,
      BC.Project,
      MAX(BC.OrderId) AS MaxOrderId
    FROM
      BranchCommits AS BC
    WHERE
      BC.IsFirstParent = True
      AND (
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
    GROUP BY
      BC.DownstreamName,
      BC.DownstreamBranch,
      BC.UpstreamName,
      BC.UpstreamBranch,
      BC.Project
  ) AS Latest ON BC.DownstreamName = Latest.DownstreamName
  AND BC.DownstreamBranch = Latest.DownstreamBranch
  AND BC.UpstreamName = Latest.UpstreamName
  AND BC.UpstreamBranch = Latest.UpstreamBranch
  AND BC.Project = Latest.Project
  AND BC.OrderId = Latest.MaxOrderId
WHERE
  BC.IsFirstParent = True
  AND (
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