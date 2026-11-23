package main

import (
    "errors"
    "fmt"
    "sort"
    "strings"
)

// pendingTags returns *all* stable application releases not yet installed,
// oldest first. Skipping directly to the latest tag can break before_sha256.
func pendingTags(releases []release, current string) ([]string, error) {
    if current != "" && !tagRx.MatchString(current) {
        return nil, errors.New("invalid locally installed tag")
    }
    tags := []string{}
    seen := make(map[string]bool)
    for _, r := range releases {
        if r.IsDraft || r.IsPrerelease || !tagRx.MatchString(r.TagName) {
            continue
        }
        if seen[r.TagName] {
            return nil, fmt.Errorf("duplicate published release: %s", r.TagName)
        }
        seen[r.TagName] = true
        tags = append(tags, r.TagName)
    }
    sort.Slice(tags, func(i, j int) bool { return compareTags(tags[i], tags[j]) < 0 })
    for i := 1; i < len(tags); i++ {
        if compareTags(tags[i-1], tags[i]) == 0 {
            return nil, errors.New("ambiguous published semantic versions")
        }
    }
    out := []string{}
    for _, tag := range tags {
        if current == "" || compareTags(tag, current) > 0 {
            out = append(out, tag)
        }
    }
    // Protect against an unexpectedly large release train.
    if len(out) > 25 {
        return nil, fmt.Errorf("too many pending releases (%d); manual review required", len(out))
    }
    return out, nil
}

// combinePackages validates a complete, ordered release chain and reduces it
// to one file-only transaction. Each file preserves the ORIGINAL before_sha256
// and the FINAL payload/sha256. The actual Cloudware source is checked by
// preflight before anything is changed.
func combinePackages(packages []pkg) (pkg, error) {
    if len(packages) == 0 {
        return pkg{}, errors.New("release chain empty")
    }
    type fileState struct {
        initial string
        latest  patchFile
        data    []byte
    }
    states := map[string]fileState{}
    serviceSet := map[string]bool{}
    for i, p := range packages {
        if err := validateManifest(p.Manifest, p.Manifest.Tag); err != nil {
            return pkg{}, fmt.Errorf("release %d invalid: %w", i+1, err)
        }
        if i > 0 && compareTags(packages[i-1].Manifest.Tag, p.Manifest.Tag) >= 0 {
            return pkg{}, errors.New("release packages not in ascending version order")
        }
        if len(p.Data) != len(p.Manifest.Files) {
            return pkg{}, errors.New("release payload file count mismatch")
        }
        for _, service := range p.Manifest.Services {
            serviceSet[service] = true
        }
        for _, f := range p.Manifest.Files {
            data, ok := p.Data[f.Path]
            if !ok || shaBytes(data) != f.SHA256 {
                return pkg{}, fmt.Errorf("payload SHA256 mismatch: %s", f.Path)
            }
            key := strings.ToLower(f.Path)
            old, found := states[key]
            if found {
                if old.latest.Path != f.Path {
                    return pkg{}, fmt.Errorf("path case changed across releases: %s", f.Path)
                }
                if old.latest.SHA256 != f.BeforeSHA256 {
                    return pkg{}, fmt.Errorf("broken release chain at %s", f.Path)
                }
                states[key] = fileState{initial: old.initial, latest: f, data: data}
            } else {
                states[key] = fileState{initial: f.BeforeSHA256, latest: f, data: data}
            }
        }
    }
    lastTag := packages[len(packages)-1].Manifest.Tag
    merged := pkg{Manifest: manifest{Schema: 1, Tag: lastTag}, Data: map[string][]byte{}}
    for service := range serviceSet {
        merged.Manifest.Services = append(merged.Manifest.Services, service)
    }
    sort.Strings(merged.Manifest.Services)
    for _, state := range states {
        finalFile := state.latest
        finalFile.BeforeSHA256 = state.initial
        merged.Manifest.Files = append(merged.Manifest.Files, finalFile)
        merged.Data[finalFile.Path] = state.data
    }
    sort.Slice(merged.Manifest.Files, func(i, j int) bool {
        return merged.Manifest.Files[i].Path < merged.Manifest.Files[j].Path
    })
    merged.Manifest.Notes = fmt.Sprintf("Combined %d verified releases; final %s", len(packages), lastTag)
    if err := validateManifest(merged.Manifest, lastTag); err != nil {
        return pkg{}, fmt.Errorf("combined manifest invalid: %w", err)
    }
    return merged, nil
}
