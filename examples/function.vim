function! s:f1() abort
endfunction
function! s:f2() abort
endfunction
function! s:f3()
endfunction
function! s:f4() abort
" 1
endfunction
function! s:f5() abort
" 2
endfunction
function! s:f6()
" 3
endfunction
function! s:f7() abort
" 42
endfunction
function! s:f8() abort
" 42
endfunction
function! s:f9() abort
" 42
endfunction
function! s:f10() abort
" 42
endfunction
function! s:f11() abort
" 42
endfunction
function! s:f12() abort
" 42
endfunction
function! s:f13() abort
" 42
endfunction
" {->return 42}
" {->1}
" {->2}
" 
" {->42}
" 
" {->42}
" 
" function('s:expr1')
" function('s:expr2')
" function('s:expr3')
" function('s:expr4')
" function('s:expr5')
" function('s:expr6')
" function('s:expr7')
" function('s:expr8')
" function('s:expr9')
" function('s:expr10')
" function('s:expr11')
" function('s:expr12')
" function('s:expr13')
" {->return 42}
" {->1}
" {->2}
" 
" {->42}
" 
" {->42}
" 
function! s:func_with_type() abort
endfunction