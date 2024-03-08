package spec 

import (
    "time"
)

// TODO: consider adding defaults to more fields

// This alias exists for the sake of being explicit, and in case we want to add restrictions later
// for what kind of filepaths we allow in Dalec
let filepath = string

// {0, ..., 511} = {0o000, ..., 0o777} are valid unix perms
let perms = >= 0 & <= 0o777

// A source name must be alphanumeric, with the inclusion of '_', '-', and '.'
let sourceName = =~ "^[a-zA-Z0-9_-|.]+$"
let buildVar = =~ "^\\${[a-zA-Z_][a-zA-Z0-9_]*}$"

#BuildStep: {
   command: string
   env?: [string]: string 
}

#CacheDirConfig: {
    mode?: "shared" | "private" | "locked" 
    key?: string 
    include_distro_key?: bool 
    include_arch_key?: bool 
}

#Command: {
   dir?: filepath 
   mounts?: [...#SourceMount] 
   cache_dirs?: [filepath]: #CacheDirConfig 
   env?: [string]: string 
   steps: [...#BuildStep] 
}

#SourceMount: {
    dest: filepath 
    // structural cycle formed by source.image.mounts.spec.source must terminate somehow for cue to accept
    // even though there are non-recursive source variants so there is implicitly a base case, 
    // cue's cycle detection is not currently buggy in this case
    spec: null | #Source 
}

#SourceContext: {
    name?: string
}

#SourceDockerImage: { 
    ref: string 
    cmd?: #Command 
}

#SourceHTTP: {
    // TODO: specify url field further?
    url: string 
}

#SourceBuild: {
    source?: #SubSource 
    dockerfile_path?: filepath 
    target?: string 
    args?: [string]: string 
}

#SourceGit: {
    // TODO: specify URL field further?
    url: string 
    commit?: string 
    keepGitDir?: bool 
}

#SourceInlineFile: {
    contents?: string 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
}

#SourceInlineDir: {
    files?: [sourceName]: #SourceInlineFile 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
}

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

#PackageDependencies: {
    build?: [string]: (null | [...string]) 
    runtime?: [string]: (null | [...string]) 
    recommends?: [string]: (null | [...string]) 
    test?: [string]: (null | [...string]) 
}

#ArtifactBuild: {
    steps: [...#BuildStep]  
    env?: [string]: string 
}

#ArtifactConfig: {
    subpath?: filepath 
    name?: string 
}

#Artifacts: {
    binaries?: [filepath]: (null | #ArtifactConfig)
    manpages?: [filepath]: (null | #ArtifactConfig)
}

#CheckOutput: {
    equals?: string 
    contains?: [...string] 
    matches?: string 
    starts_with?: string 
    ends_with?: string 
    empty?: bool 
 }

#TestStep: {
    command?: string 
    env?: [string]: string 
    stdout?: #CheckOutput 
    stderr?: #CheckOutput 
    stdin?: string 
}

#FileCheckOutput: {
    #CheckOutput 
    permissions?: perms 
    is_dir?: bool 
    not_exist?: bool 
}

#TestSpec: {
    name: string 
    dir?: filepath 
    mounts?: [...#SourceMount] 
    cacheDirs?: [string]: #CacheDirConfig 
    env?: [string]: string 
    steps: [...#TestStep] 
    files?: [filepath]: (null | #FileCheckOutput)
}

#SymlinkTarget: {
    path: filepath 
}

#PostInstall: {
    symlinks?: [filepath]: #SymlinkTarget 
}

#ImageConfig: {
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
}

#Frontend: {
    image: string 
    cmdline?: string 
}

#Target: {
    dependencies?: (null | #PackageDependencies)
    image?: #ImageConfig
    frontend?: #Frontend
    tests?: [...#TestSpec]
}

#PatchSpec: {
    source: sourceName 
    strip?: int 
}

#ChangelogEntry: {
    date: time.Time
    author: string
    changes: [...string]
}

#Spec: {
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

    // TODO: could probably validate magic variables here,
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
}

#Spec