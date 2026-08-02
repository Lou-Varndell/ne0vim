# rename utility written in python

## original bash script has grown in size and complexity

```bash
mapfile -t files < <(
    find . -maxdepth 1 -type f -print0 |
    while IFS= read -r -d '' file; do
        file=${file#./}

        birth=$(stat -f '%B' "$file")
        if (( birth > 0 )); then
            printf '%s\t%s\n' "$birth" "$file"
        else
            stat -f '%m\t%N' "$file"
        fi
    done |
    sort -n -k1,1 -k2,2 |
    cut -f2-
    # find . -maxdepth 1 -type f -print0 |
    # while IFS= read -r -d '' file; do
    #     file=${file#./}
    #     stat -f '%B\t%N' "$file" # stat -f '%m\t%N' "$file"
    # done |
    # sort -n -k1,1 -k2,2 |
    # cut -f2-
)

(( ${#files[@]} )) || exit 0

# Width of the new numbers (minimum 3 digits).
width=${#files[@]}
(( width < 3 )) && width=3

declare -a renames

i=1
for old in "${files[@]}"; do
    [[ $old =~ ^(.*)_([0-9]+)\.([^.]+)$ ]] || continue

    prefix=${BASH_REMATCH[1]}
    ext=${BASH_REMATCH[3]}

    printf -v new '%s_%0*d.%s' \
        "$prefix" \
        "$width" \
        "$i" \
        "$ext"

    if [[ $old != "$new" ]]; then
        tmp=".$old.$$"
        mv -v -- "$old" "$tmp"
        renames+=("$tmp"$'\t'"$new")
    fi

    ((i++))
done

# Final rename pass.
for entry in "${renames[@]}"; do
    IFS=$'\t' read -r tmp new <<<"$entry"
    mv -v -- "$tmp" "$new"
done
```

## write a python script to replace and expand the bash script
- keep this utility completely dependency-free—just the Python standard library and mark executable with #!/usr/bin/env python3.
- A standalone, dependency-free rename.py that only renames files safely and predictably.

## use Python 3.14+ and Standard library only
- pathlib
- argparse

```
#!/usr/bin/env python3
```
## Collision-safe temporary renames

## arguments
- --dry-run
- --verbose

## Every execution creates JSON undo log
- .rename.undo.json that restores everything with --undo.
```json
[
    {
        "old":"foo_017.jpg",
        "new":"foo_001.jpg"
    },
    ...
]
```

## Automatic padding
- unless overridden with --width

## Birth time fallback to mtime
