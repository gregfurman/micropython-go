# Vendors the starlark-go conformance corpus into testdata/starlark.

Q ?= @

STARLARK_SHA    := 5395d018f003e2a08bfbca6dcb2562acee700f62
STARLARK_DIR    := testdata/starlark
STARLARK_URL    := https://github.com/google/starlark-go/archive/$(STARLARK_SHA).tar.gz
STARLARK_ASSERT := https://raw.githubusercontent.com/google/starlark-go/$(STARLARK_SHA)/starlarktest/assert.star

.PHONY: starlark-testdata
starlark-testdata:
	$(Q)rm -rf $(STARLARK_DIR) && mkdir -p $(STARLARK_DIR)
	$(Q)curl -fsSL $(STARLARK_URL) | tar -xz -C $(STARLARK_DIR) --strip-components=3 \
		--exclude='*/proto' --exclude='*/proto.star' \
		starlark-go-$(STARLARK_SHA)/starlark/testdata
	$(Q)curl -fsSL -o $(STARLARK_DIR)/assert.star $(STARLARK_ASSERT)

clean:
	$(Q)rm -rf $(STARLARK_DIR)
