all: model/trivial.so model/trivial2.so \
	model/no_init.so model/wrong_init.so model/error_init.so \
	model/no_req.so model/wrong_req.so model/error_req.so \
	model/param.so \
	decision/test.so \
	decision/no_check.so decision/wrong_check.so decision/error_check.so \
	decision/simple.so

# Pass COVER=1 to build plugins instrumented for coverage:
#   make -B COVER=1
# This is required when running: go test -cover ./...
ifdef COVER
FLAGS += -cover
endif

%.so: %.go
	go build $(FLAGS) -buildmode=plugin -o $@ $<

clean:
	rm -f model/*.so decision/*.so
