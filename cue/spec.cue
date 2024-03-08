package spec 

import (
    "time"
)

// This alias exists for the sake of being explicit, and in case we want to add restrictions later
// on what kind of filepaths we allow in Dalec
let filepath = string

// {0, ..., 511} = {0o000, ..., 0o777} are valid unix perms
let perms = >= 0 & <= 0o777

// A source name must be alphanumeric, with the inclusion of '_', '-', and '.'
let sourceName = =~ "^[a-zA-Z0-9_-|.]+$"
let buildVar = =~ "^\\${[a-zA-Z_][a-zA-Z0-9_]*}$"

#BuildStep: close({
   command: string
   env?: [string]: string 
})

#CacheDirConfig: close({
    mode?: "shared" | "private" | "locked" 
    key?: string 
    include_distro_key?: bool 
    include_arch_key?: bool 
})

#Command: close({
   dir?: filepath 
   mounts?: [...#SourceMount] 
   cache_dirs?: [filepath]: #CacheDirConfig 
   env?: [string]: string 
   steps: [...#BuildStep] 
})

#SourceMount: {
    dest: filepath 
    // structural cycle formed by source.image.mounts.spec.source must terminate somehow for cue to accept
    // even though there are non-recursive sources, (i.e., it is not required that SourceMount contain a recursive source, so there is
    // implicitly a base case), cue's cycle detection is not currently sufficient to detect this.
    spec: null | #Source 
}

#SourceContext: close({
    name?: string
}) 

#SourceDockerImage: close({ 
    ref: string 
    cmd?: #Command 
})

#SourceHTTP: close({
    // TODO: specify url field further?
    url: string 
})

#SourceBuild: close({
    source?: #SubSource 
    dockerfile_path?: filepath 
    target?: string 
    args?: [string]: string 
})

#SourceGit: {
    // TODO: specify URL field further?
    url: string 
    commit?: string 
    keepGitDir?: bool 
}

#SourceInlineFile: close({
    contents?: string 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
})

#SourceInlineDir: close({
    files?: [sourceName]: #SourceInlineFile 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
})

#SourceInlineVariant: { file: #SourceInlineFile } | 
                      { dir: #SourceInlineDir } 
#SourceInline: { inline: #SourceInlineVariant }

#SourceVariant: { context: #SourceContext } | 
                { git: #SourceGit } |
                { build: #SourceBuild } |
                { http: #SourceHTTP } |
                { image: #SourceDockerImage } |
                { inline: #SourceInlineVariant }

// these are sources which are suitable as a sub-source for
// SourceBuild's .source
#SubSourceVariant: { context: #SourceContext } | 
                   { git: #SourceGit } |
                   { http: #SourceHTTP } |
                   { image: #SourceDockerImage } |
                    #SourceInline 

// base properties which are source-independent
#SourceBase: { path?: filepath 
               includes?: [...string] 
               excludes?: [...string] 
            }

#Source: {#SourceBase, #SourceVariant}
#SubSource: {#SourceBase, #SubSourceVariant}

#PackageDependencies: close({
    build?: [string]: (null | [...string]) 
    runtime?: [string]: (null | [...string]) 
    recommends?: [string]: (null | [...string]) 
    test?: [string]: (null | [...string]) 
})

#ArtifactBuild: close({
    steps: [...#BuildStep]  
    env?: [string]: string 
})

#ArtifactConfig: close({
    subpath?: filepath 
    name?: string 
})

#Artifacts: close({
    binaries?: [filepath]: (null | #ArtifactConfig)
    manpages?: [filepath]: (null | #ArtifactConfig)
})

#CheckOutput: close({
    equals?: string 
    contains?: [...string] 
    matches?: string 
    starts_with?: string 
    ends_with?: string 
    empty?: bool 
 })

#TestStep: close({
    command?: string 
    env?: [string]: string 
    stdout?: #CheckOutput 
    stderr?: #CheckOutput 
    stdin?: string 
})

#FileCheckOutput: {
    #CheckOutput 
    permissions?: perms 
    is_dir?: bool 
    not_exist?: bool 
}

#TestSpec: close({
    name: string 
    dir?: filepath 
    mounts?: [...#SourceMount] 
    cacheDirs?: [string]: #CacheDirConfig 
    env?: [string]: string 
    steps: [...#TestStep] 
    files?: [filepath]: (null | #FileCheckOutput)
})

#SymlinkTarget: close({
    path: filepath 
})

#PostInstall: close({
    symlinks?: [filepath]: #SymlinkTarget 
})

#ImageConfig: close({
    entrypoint?: string 
    cmd?: string 
    env?: [...string] 
    labels?: [string]: string 
    volumes?: [string]: null 
    working_dir?: string 
    stop_signal?: string 
    base?: string 
    post?: #PostInstall
    user?: string 
})

#Frontend: close({
    image: string 
    cmdline?: string 
})

#Target: close({
    dependencies?: (null | #PackageDependencies)
    image?: #ImageConfig // c
    frontend?: #Frontend // c
    tests?: [...#TestSpec] // c
})

#PatchSpec: close({
    source: sourceName 
    strip?: int 
})

#ChangelogEntry: close({
    date: time.Time
    author: string
    changes: [...string]
})

#Spec: close({
    name: string | *"My Package"
    description: string | *"My Dalec Package"
    website?: string 
    version: string | *"0.1"
    revision: (buildVar | uint) | *1
    noarch?: bool 

    conflicts?: [string]: (null | [...string])
    replaces?: [string]: (null | [...string])
    provides?: [...string] 

    sources?: [sourceName]: #Source 

    patches?: [sourceName]: [...#PatchSpec] 

    build?: #ArtifactBuild  

    // should probably validate magic variables here 
    // TARGET* vars should not have any default values applied
    args?: [string]: (string | int | null) 

    license: string | *"Needs License"

    vendor: string | *"My Vendor"

    packager: string | *"Azure Container Upstream"

    artifacts?: #Artifacts  

    targets?: [string]: #Target 

    dependencies?: #PackageDependencies 

    image?: #ImageConfig 

    changelog?: [...#ChangelogEntry] 

    tests?: [...#TestSpec] 
})

#Spec