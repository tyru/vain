scriptencoding utf-8
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
let echo = 
while 42
  echo("hello")
  echo("what's up")
endwhile
for v in [1,2,3]
  echo("hey:" + v)
  echo("yo")
endfor
let range = 
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