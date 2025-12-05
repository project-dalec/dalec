# Testing

Dalec supports adding tests to your spec file.
These tests are run against the container produced by your spec.
Dalec provides a few test helpers to make it easier to write tests when your image doesn't have the tools you need.

## Checks for files


### Check file existence

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /usr/bin/foo:
```

### Check file contents

Here are some examples on how to check the contents and metadata of a file in the output container.
You can use these to check that the files you expect are present and have the correct contents.


#### Check that a file contains some value:

Here we check that the content of the file `/etc/foo.conf` contains the string `foo=bar`.
You can specify multiple values to check for.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        contains:
          - foo=bar
```


#### Check that a file starts with some value:

Here we check that the content of the file `/etc/foo.conf` starts with the string `foo`.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        starts_with: foo
```


#### Check that a file ends with some value:

Here we check that the content of the file `/etc/foo.conf` ends with the string `bar`.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        ends_with: bar
```


#### Check that a file matches a regular expression:

Here we check that the content of the file `/etc/foo.conf` matches one of the regular expressions (in this case only `foo=.*`).

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        matches: 
          - "foo=.*"
```

#### Check that a file does not exist

Here we check that the file `/some/nonexistent/path` does not exist.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /some/nonexistent/path:
        not_exist: true
```

#### Check that a path is a directory

Here we check that the path `/some/path` is a directory.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /some/path:
        is_dir: true
```

#### Check file permissions

Here we check that the file `/etc/foo.conf` has the permissions `0644`.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        contains:
          - foo=bar
        starts_with: foo
        ends_with: bar
        matches: 
          - "foo=.*"
        permissions: 0644
```

#### Add multiple checks together

You can add multiple checks together to check multiple things about a file.
Note that some checks are mutually exclusive and will cause an error if you try to use them together.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    files:
      /etc/foo.conf:
        contains:
          - foo=bar
        starts_with: foo
        ends_with: bar
        matches: 
          - "foo=.*"
        permissions: 0644
```

#### Symlinks

To check the target of a symlink set the `link_target` field:

```yaml
tests:
  - name: My Test case
    files:
      /some/path:
        link_target: /expected/target
```

### Run a command

You can run a command in the container and check the stdout and/or stderr of that command.
These commands are run before any of the file checks are run and may influence the output of those checks.

Because these images often will not have a shell in them, if you want a shell you'll need to run it explicitly.


#### Capture stdio streams

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    steps:
      - command: echo "hello world"
        stdout:
          equals:
            - hello world\n
      - # Note: the image used for this test would need to have a shell in it for this to work
        command: /bin/sh -c 'echo "hello world" >&2'
        stderr:
          equals:
            - hello world\n
```

Pass in stdin to the command:

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    steps:
      - command: cat
        stdin: hello world
        stdout:
          equals:
            - hello world
```

#### Inject a file into the container for additional checks

Test cases support source mounts just like in the main spec.
You can use this to inject files, build helper binaries, or add whatever you need to run a test.

```yaml
name: My Package
# ... other spec fields

tests:
  - name: My Test case
    mounts:
      - dest: /target/mount/path
        spec:
          build:
            source:
              inline:
                file:
                  contents: |
                    FROM busybox
                    RUN echo hello > /hello

                    FROM scratch
                    COPY --from=busybox /hello /hello
    steps:
      - command: cat /path/in/container
        stdout:
          equals: hello
```
