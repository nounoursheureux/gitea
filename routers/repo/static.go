// Copyright 2015 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
    "log"
    "io/ioutil"
    "path"
    "mime"
    "github.com/go-gitea/gitea/modules/middleware"
)

func Static(ctx *middleware.Context) {
    if ctx.Repo.GitRepo.IsBranchExist("gitea-pages") == true {
        serve("gitea-pages", ctx)
    } else if ctx.Repo.GitRepo.IsBranchExist("gh-pages") == true {
        serve("gh-pages", ctx)
    } else {
        ctx.Handle(404, "repo.NotFound", nil)
    }
}

func serve(branch string, ctx *middleware.Context) {
    commit, err := ctx.Repo.GitRepo.GetCommitIdOfBranch(branch)
    if err != nil {
        log.Fatal("Couldn't get commit ID")
    }
    tree, err := ctx.Repo.GitRepo.GetTree(commit)
    if err != nil {
        log.Fatal("Couldn't get tree")
    }
    var filepath string
    if ctx.Params("*") != "" {
        filepath = ctx.Params("*")
    } else {
        filepath = "index.html"
    }
    blob, err := tree.GetBlobByPath(filepath)
    if err != nil {
        ctx.Handle(404, "repo.NotFound", nil)
        return
    }
    data, err := blob.Data()
    if err != nil {
        log.Fatal("Couldn't get the data")
    }
    content, err := ioutil.ReadAll(data)
    if err != nil {
        log.Fatal("Couldn't read the data")
    }
    mediatype := mime.TypeByExtension(path.Ext(blob.Name()))
    ctx.Resp.Header().Set("Content-Type", mediatype)
    ctx.Write(content)
}
