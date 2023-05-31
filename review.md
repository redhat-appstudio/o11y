# Code Review principles and tips

## Why do we code review?

1. Code reviews are essential to improve the level of our code, and to prevent bugs. 

2. A great and fast way for both the reviewer and the code owner to share ideas, 
   learn, and improve their code writing and understanding skills.

3. Allow us as reviewers to get to know parts of the code base we usually
   don't work on.

4. Help our fellow team members to validate their code quality and push updates faster.

## Tips for reviewing

-   It is important to maintain a healthy, respectful, and civil environment. Feel free
    to ask questions, offer suggestions, or provide criticism, regardless of your
    position or level of expertise. However, it is crucial to avoid personal attacks
    and focus on reviewing the code rather than criticizing the individual who wrote it.
    Additionally, please refrain from using capital letters (all caps) and inappropriate 
    language
    Here are a few examples:

| Good feedback                                                                                                      | Bad feedback                                 |
|--------------------------------------------------------------------------------------------------------------------|----------------------------------------------|
| "Maybe we can rephrase this to be more along those lines of: "                                                     | "This is a mess, change it to: "             |
| "I don't believe this approach will fit what we're looking for, try finding a way of achieving it without using X” | "This code is no good, change it."           |
| "Sorry, I might have miscommunicated that, what I meant was - ... "                                                | "this is not what I meant at all"            |
| "This won't work, maybe try and look into X"                                                                       | "THIS IS WRONG."                             |
| "Missing a test for X",                                                                                            | "What is it with you and missing tests?"     |
| "I've seen this error repeated multiple times, have I mentioned X? try to look into it, it has some nice tools"    | "You keep repeating those types of mistakes" |
| What is the reason you chose this implementation?                                                                  | huh? Why did you do that?                    |


-   Don't forget that in the worst case scenario - you're wrong, and you learned something
    for the next time.


- Remember that the goal isn't solely to suggest changes; it also serves as an
  opportunity to ask questions. By asking questions, you can challenge the code owner
  and assist them in gaining a deeper understanding of their own code. When providing
  answers, code owners often realize the need for changes they hadn't considered before.
  Engaging in a discussion will benefit both you and the code owner.
  For instance, you can ask questions such as:
  - "What is the rationale behind this change?"
  - "How does this change relate to 'X', which was mentioned in the description?"
  - "What is the intended purpose of this particular section?"

  The aim is to foster constructive dialogue that enhances comprehension and cooperation
  within the team.


-   Don't be afraid to give a thumbs down or require changes multiple times! Remember
    that code reviews are focused on the code itself and should not be taken personally.
    By separating the code from personal matters, we create an environment that
    encourages growth and collaboration.


-   Try to engage in critical thinking and consider if there are any valuable suggestions
    you can offer before concluding your review with a simple "LGTM". While there may be
    instances where no changes are required, it's important to recognize that even a
    small suggestion can initiate a meaningful conversation.
    Here are some aspects to consider during the review process:
    - Pay attention to edge cases, such as race conditions, cleanups, and the presence of
      tests that cover enough use cases without becoming excessive.
    - Be on the lookout for missing tests. A good rule of thumb is that if something
      requires manual execution to see that it works, it's possible that corresponding
      tests are absent.
    - Assess whether the PR focuses on a specific subject. Lengthy or disorganized PRs
      can make the review process more challenging. It may be beneficial to break them
      down into smaller, standalone PRs, making it easier to identify errors and shorten
      the feedback loop.
    - Verify that the added functionality does not already exist within the code base.
      Take into consideration existing functions, features, or packages that could be
      utilized instead of reinventing the wheel.
    - Ensure that the commit message and PR description are clear, up-to-date, and
      effectively communicate the issue and proposed solution. While including a link
      to JIRA is helpful, it's also important to be able to comprehend and track code
      changes locally via the git log without excessive reliance on external tools like
      JIRA.
    - Assess whether the code is easily maintainable and testable, as these qualities
      contribute to the overall quality and longevity of the code base.

    By encouraging such discussions, we create opportunities for new insights and
    improvements to emerge. Therefore, let's strive to think outside of the box and
    contribute to fostering a productive exchange of ideas.


-   Examine other PRs and the comments provided by fellow reviewers. Observe how
    they respond and what aspects of the code they focus on. By doing so, you can
    gain insights into their thought process and the specific code areas they consider.
