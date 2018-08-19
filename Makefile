
all:
	go build
	rm -f examples/*.pretty
	./vain fmt examples/
	rm -f examples/*.vim
	./vain build examples/

diff:
	for i in examples/*.vain; do diff -u $$i $$i.pretty; done
