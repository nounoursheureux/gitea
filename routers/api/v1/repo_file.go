// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package v1

import (
	"github.com/go-gitea/gitea/modules/base"
	"github.com/go-gitea/gitea/modules/git"
	"github.com/go-gitea/gitea/modules/middleware"
	"github.com/go-gitea/gitea/routers/repo"
)

func GetRepoRawFile(ctx *middleware.Context) {
	if !ctx.Repo.HasAccess() {
		ctx.Error(404)
		return
	}

	blob, err := ctx.Repo.Commit.GetBlobByPath(ctx.Repo.TreeName)
	if err != nil {
		if err == git.ErrNotExist {
			ctx.Error(404)
		} else {
			ctx.JSON(500, &base.ApiJsonErr{"GetBlobByPath: " + err.Error(), base.DOC_URL})
		}
		return
	}
	if err = repo.ServeBlob(ctx, blob); err != nil {
		ctx.JSON(500, &base.ApiJsonErr{"ServeBlob: " + err.Error(), base.DOC_URL})
	}
}
