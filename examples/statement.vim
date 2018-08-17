scriptencoding utf-8
" vain: begin named expression functions
function! s:_vain_dummy_lambda1(msg) abort
endfunction
function! s:_vain_dummy_lambda2(begin,end) abort
endfunction
" vain: end named expression functions

if 1
  12
  34
elseif 2
  45
  68
else
  90
  91
endif
let echo = function('s:_vain_dummy_lambda1')
while 42
  echo("hello")
  echo("what's up")
endwhile
for v in [1,2,3]
  echo("hey:" + v)
  echo("yo")
endfor
let range = function('s:_vain_dummy_lambda2')
for n in range(1,100)
  if (n % 15) ==# 0
    echo("fizzbuzz")
  elseif (n % 5) ==# 0
    echo("buzz")
  elseif (n % 3) ==# 0
    echo("fizz")
  else
    echo(n.toString())
  endif
endfor