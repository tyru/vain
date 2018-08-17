scriptencoding utf-8


if 1
endif
function! s:f() abort
  42
endfunction
function! s:f() abort
  42
endfunction
function! s:f() abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
function! s:f(a) abort
  42
endfunction
1 ? 2 : 3

1 || 3
1 && 3
1 ==# 3
1 ==? 3
1 !=# 3
1 !=? 3
1 ># 3
1 >? 3
1 >=# 3
1 >=? 3
1 <# 3
1 <? 3
1 <=# 3
1 <=? 3
1 =~# 3
1 =~? 3
1 !~# 3
1 !~? 3
1 is# 3
1 is? 3
1 isnot# 3
1 isnot? 3
1 + 3
1 - 3
1 * 3
1 / 3
1 % 3
let foo = {}
foo["bar"]
let bar = []
bar[1:2]
let f = 
call f(1,2,3)
let obj = {}
obj.prop
[1,2,3]
{'key':'value','k1':42}